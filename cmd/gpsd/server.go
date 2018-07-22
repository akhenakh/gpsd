package main

import (
	"io"
	"log"
	"sync"
	"time"

	"github.com/akhenakh/gpsd/gpssvc"
	osrm "github.com/gojuno/go.osrm"
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
	return &Server{
		c:          pchan,
		quit:       make(chan struct{}),
		osrmClient: osrm.NewFromURL(osrmAddr),
	}, nil
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

				// map match if possible
				if len(s.lastPositions) > 2 {
					ps := make(geo.PointSet, len(s.lastPositions))
					for i := 0; i < len(s.lastPositions); i++ {
						ps[i] = geo.Point{s.lastPositions[i].Longitude, s.lastPositions[i].Latitude}
					}

					mctx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
					req := osrm.MatchRequest{
						Profile: "car",
						// TODO: transform lastPositions to pointset
						GeoPath:     *osrm.NewGeoPathFromPointSet(ps),
						Annotations: osrm.AnnotationsTrue,
					}

					resp, err := s.osrmClient.Match(mctx, req)
					if err != nil {
						log.Println(err)
					} else {

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
								log.Println(resp.Matchings[len(resp.Matchings)-1].Legs)
								if *debug {
									log.Println("matched position distance", points, last.Distance)
								}
							}
						}
					}

					cancel()
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

func (s *Server) Stop() {
	s.quit <- struct{}{}
}

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
