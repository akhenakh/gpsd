package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"sync"
	"time"

	osrm "github.com/akhenakh/go.osrm"
	"github.com/akhenakh/gpsd/gpssvc"
	"github.com/golang/protobuf/ptypes"
	google_protobuf "github.com/golang/protobuf/ptypes/empty"
	geo "github.com/paulmach/go.geo"
	"github.com/pkg/errors"
	context "golang.org/x/net/context"
)

const (
	// how many positions we want to keep in lastPositions
	// used for map matching
	keepPositionCount = 10

	// Limits the search to segments with given bearing in degrees towards true north in clockwise direction.
	searchBearing = 30
)

type Server struct {
	c          chan *gpssvc.Position
	quit       chan struct{}
	osrmClient *osrm.OSRM
	debug      bool

	// latest positions
	latestPositions []*gpssvc.Position

	sync.RWMutex
	clients []chan (*gpssvc.Position)
}

func NewServer(pchan chan *gpssvc.Position, osrmAddr string) (*Server, error) {
	s := &Server{
		c:    pchan,
		quit: make(chan struct{}),
	}
	if osrmAddr != "" {
		s.osrmClient = osrm.NewFromURL(osrmAddr)
	}

	return s, nil
}

func (s *Server) mapNear(ctx context.Context, p *gpssvc.Position) error {
	mctx, cancel := context.WithTimeout(ctx, 30*time.Millisecond)
	defer cancel()

	req := osrm.NearestRequest{
		Profile:  "car",
		GeoPath:  *osrm.NewGeoPathFromPointSet(geo.PointSet{*geo.NewPointFromLatLng(p.Latitude, p.Longitude)}),
		Number:   1,
		Bearings: []osrm.Bearing{{Value: uint16(p.Heading), Range: searchBearing}},
	}

	resp, err := s.osrmClient.Nearest(mctx, req)
	if err != nil {
		return err
	}
	if len(resp.Waypoints) > 0 {
		p.Matched = true
		p.MatchedLatitude = resp.Waypoints[0].Location.Lat()
		p.MatchedLongitude = resp.Waypoints[0].Location.Lng()
		p.MatchedDistance = resp.Waypoints[0].Distance
		// near does not compute the corrected heading
		p.MatchedHeading = p.Heading
		p.RoadName = resp.Waypoints[0].Name
		if s.debug {
			log.Println("map near position distance", p.MatchedLatitude, p.MatchedLongitude, p.MatchedDistance)
		}
	}
	return nil
}

// mapMatch use latest known positions to fix p
// returns true if the position can be map matched
// modify position with map matched data
func (s *Server) mapMatch(ctx context.Context, p *gpssvc.Position, latestPos []*gpssvc.Position) error {
	mctx, cancel := context.WithTimeout(ctx, 30*time.Millisecond)
	defer cancel()

	prevPos := latestPos[:]
	prevPos = append(prevPos, p)

	ps := make(geo.PointSet, len(prevPos))
	for i := 0; i < len(prevPos); i++ {
		ps[i] = geo.Point{prevPos[i].Longitude, prevPos[i].Latitude}
	}

	// create the radiuses using horizontal precision
	radiuses := make([]float64, len(prevPos))
	for i := 0; i < len(prevPos); i++ {
		radiuses[i] = prevPos[i].HorizontalPrecision
	}

	// create the timestamps
	tss := make([]int64, len(prevPos))
	for i := 0; i < len(prevPos); i++ {
		t, err := ptypes.Timestamp(prevPos[i].Ts)
		if err != nil {
			return errors.Wrap(err, "can't read event time ts back")
		}
		tss[i] = t.Unix()
	}

	// create the bearings
	bs := make([]osrm.Bearing, len(prevPos))
	for i := 0; i < len(prevPos); i++ {
		bs[i] = osrm.Bearing{Value: uint16(prevPos[i].Heading), Range: searchBearing}
	}

	// create the hints
	hints := make([]string, len(prevPos))
	for i := 0; i < len(prevPos); i++ {
		hints[i] = prevPos[i].Hint
	}

	req := osrm.MatchRequest{
		Profile: "car",
		GeoPath: *osrm.NewGeoPathFromPointSet(ps),
		//Annotations: osrm.AnnotationsTrue,
		//Overview:    osrm.OverviewFull,
		Radiuses:   radiuses,
		Timestamps: tss,
		Bearings:   bs,
		Hints:      hints,
	}

	resp, err := s.osrmClient.Match(mctx, req)
	if err != nil {
		return err
	}

	if len(resp.Matchings) > 0 {
		last := resp.Matchings[len(resp.Matchings)-1]
		points := last.Geometry.Points()
		if len(points) > 0 {
			p.Matched = true
			p.MatchedLatitude = points[0].Lat()
			p.MatchedLongitude = points[0].Lng()
			// TODO: p.MatchedHeading =
			p.MatchedDistance = float64(last.Distance)
			if len(resp.Tracepoints) > 0 {
				p.Hint = resp.Tracepoints[len(resp.Tracepoints)-1].Hint
			}
			//log.Println(resp.Matchings[len(resp.Matchings)-1].Legs)
			if s.debug {
				log.Println("map matched position distance", points[0], last.Distance)
			}
		}
	}
	return nil
}

func (s *Server) Start() {
	go func() {
		ctx, cancel := context.WithCancel(context.Background())
		for {
			select {
			case <-s.quit:
				cancel()
				return
			case p := <-s.c:
				// near match
				if s.osrmClient != nil {
					// map match if possible
					if len(s.latestPositions) >= 1 {
						err := s.mapMatch(ctx, p, s.latestPositions)
						if err != nil {
							log.Println(err)
						}
					} else {
						err := s.mapNear(ctx, p)
						if err != nil {
							log.Println(err)
						}
					}
				}

				// try to only keep useful positions
				if p.Speed > 3 && p.HorizontalPrecision < 250 {
					s.latestPositions = append(s.latestPositions, p)
					if len(s.latestPositions) > keepPositionCount {
						s.latestPositions = s.latestPositions[len(s.latestPositions)-keepPositionCount:]
					}
				}

				if s.debug {
					log.Println("sending position", p)
				}

				s.RLock()
				for _, c := range s.clients {
					c <- p
				}
				s.RUnlock()
			}
		}
	}()
}

// Stop the server
func (s *Server) Stop() {
	s.quit <- struct{}{}
}

// LivePosition streams Position updates
func (s *Server) LivePosition(empty *google_protobuf.Empty, stream gpssvc.GPSSVC_LivePositionServer) error {
	c := make(chan (*gpssvc.Position), 1)
	s.Lock()
	s.clients = append(s.clients, c)
	s.Unlock()

	for {
		p := <-c
		err := stream.Send(p)
		if err == io.EOF {
			break
		}
		if err != nil {
			log.Println(err)
			break
		}
	}
	s.Lock()
	var b []chan (*gpssvc.Position)
	for _, elem := range s.clients {
		if elem != c {
			b = append(b, elem)
		}
	}
	s.clients = b
	close(c)
	s.Unlock()
	return nil
}

// InjectPosition Inject fake position
// use it for debug purpose only
func (s *Server) InjectPosition(ctx context.Context, p *gpssvc.Position) (*google_protobuf.Empty, error) {
	s.RLock()
	for _, c := range s.clients {
		c <- p
	}
	s.RUnlock()
	return &google_protobuf.Empty{}, nil
}

// SSEHandler an HTTP SSE handler to push positions to HTTP clients
func (s *Server) SSEHandler(rw http.ResponseWriter, req *http.Request) {
	// Make sure that the writer supports flushing.
	flusher, ok := rw.(http.Flusher)
	if !ok {
		http.Error(rw, "Streaming unsupported!", http.StatusInternalServerError)
		return
	}

	rw.Header().Set("Content-Type", "text/event-stream")
	rw.Header().Set("Cache-Control", "no-cache")
	rw.Header().Set("Connection", "keep-alive")
	rw.Header().Set("Access-Control-Allow-Origin", "*")

	// Listen to connection close
	closeChan := rw.(http.CloseNotifier).CloseNotify()

	c := make(chan (*gpssvc.Position), 1)
	s.Lock()
	s.clients = append(s.clients, c)
	s.Unlock()

Loop:
	for {
		select {
		case <-closeChan:
			fmt.Println("closed")
			break Loop
		case p := <-c:
			// Write to the ResponseWriter
			// Server Sent Events compatible
			data, _ := json.Marshal(p)

			fmt.Fprintf(rw, "data: %s\n\n", data)
			// Flush the data inmediatly instead of buffering it for later.
			flusher.Flush()
		}
	}

	s.Lock()
	var b []chan (*gpssvc.Position)
	for _, elem := range s.clients {
		if elem != c {
			b = append(b, elem)
		}
	}
	s.clients = b
	close(c)
	s.Unlock()
}
