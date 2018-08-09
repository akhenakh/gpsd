package main

import (
	"io"
	"log"
	"sync"
	"time"

	osrm "github.com/akhenakh/go.osrm"
	"github.com/akhenakh/gpsd/gpssvc"
	google_protobuf "github.com/golang/protobuf/ptypes/empty"
	geo "github.com/paulmach/go.geo"
	context "golang.org/x/net/context"
)

// how much positions we want to keep in lastPositions
// used for map matching
const keepPositionCount = 5

type Server struct {
	c          chan *gpssvc.Position
	quit       chan struct{}
	osrmClient *osrm.OSRM
	debug      bool

	// latest raw position
	lastPositions []*gpssvc.Position

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
		Bearings: []osrm.Bearing{{Value: uint16(p.Heading), Range: 40}},
	}

	resp, err := s.osrmClient.Nearest(mctx, req)
	if err != nil {
		return err
	}
	if len(resp.Waypoints) == 1 {
		p.Matched = true
		p.MatchedLatitude = resp.Waypoints[0].Location.Lat()
		p.MatchedLongitude = resp.Waypoints[0].Location.Lng()
		p.MatchedDistance = float64(resp.Waypoints[0].Distance)
		p.MatchedHeading = p.Heading
		p.RoadName = resp.Waypoints[0].Name
		if s.debug {
			log.Println("map near position distance", p.MatchedLatitude, p.MatchedLongitude, p.MatchedDistance)
		}
	}
	return nil
}

func (s *Server) mapMatch(ctx context.Context, p *gpssvc.Position) error {
	mctx, cancel := context.WithTimeout(ctx, 30*time.Millisecond)
	defer cancel()

	ps := make(geo.PointSet, len(s.lastPositions))
	for i := 0; i < len(s.lastPositions); i++ {
		ps[i] = geo.Point{s.lastPositions[i].Longitude, s.lastPositions[i].Latitude}
	}

	req := osrm.MatchRequest{
		Profile: "car",
		// TODO: transform lastPositions to pointset
		GeoPath:     *osrm.NewGeoPathFromPointSet(ps),
		Annotations: osrm.AnnotationsTrue,
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
			p.MatchedHeading = p.Heading
			p.MatchedDistance = float64(last.Distance)
			//log.Println(resp.Matchings[len(resp.Matchings)-1].Legs)
			if s.debug {
				log.Println("map matched position distance", points, last.Distance)
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

				if s.debug {
					log.Println("received raw position", p)
				}
				s.lastPositions = append(s.lastPositions, p)
				if len(s.lastPositions) > keepPositionCount {
					s.lastPositions = s.lastPositions[len(s.lastPositions)-keepPositionCount:]
				}

				// near match
				if s.osrmClient != nil {
					err := s.mapNear(ctx, p)
					if err != nil {
						log.Println(err)
					}
				}

				// map match if possible
				if len(s.lastPositions) > 10 {
					//TODO: real map match
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
