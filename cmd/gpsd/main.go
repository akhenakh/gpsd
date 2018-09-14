package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"syscall"
	"time"

	"github.com/akhenakh/gpsd/gpssvc"
	"github.com/tarm/serial"
	"google.golang.org/grpc"
)

var (
	device     = flag.String("device", "/dev/ttyACM0", "Device path")
	speed      = flag.Int("speed", 38400, "Speed in bauds")
	timeout    = flag.Duration("timeout", 4*time.Second, "Timeout in second")
	osrmAddr   = flag.String("osmrAddr", "http://localhost:5000", "OSRM API address")
	grpcPort   = flag.Int("grpcPort", 9402, "grpc port to listen")
	fakePath   = flag.String("fakePath", "", "fake NMEA data file for debug")
	fakeCount  = flag.Int("fakeCount", 3, "how many fake NMEA lines per sequences")
	logNMEA    = flag.Bool("logNMEA", false, "log all NMEA output in current directory")
	adjustTime = flag.Bool("adjustTime", false, "adjust time from GPS")
	debug      = flag.Bool("debug", false, "enable debug")
)

func main() {
	log.SetFlags(log.Lshortfile | log.Ltime)
	flag.Parse()

	// adjustTime needs some privileges
	// under Linux the CAP_SYS_TIME capability is required
	if *adjustTime {
		tv := &syscall.Timeval{}
		syscall.Gettimeofday(tv)
		err := syscall.Settimeofday(tv)
		if err != nil {
			log.Fatal("no priviles to set the date ", err)
		}
	}

	c := &serial.Config{
		Name:        *device,
		Baud:        *speed,
		ReadTimeout: *timeout,
	}

	pchan := make(chan *gpssvc.Position)

	if *fakePath != "" {
		log.Println("Using fake NMEA data", *fakePath)
		go func() {
			file, err := os.Open(*fakePath)
			if err != nil {
				log.Fatal(err)
			}
			defer file.Close()
			cr := NewChanReader()
			go func() {
				err = parseNMEA(cr, pchan)
				if err != nil {
					log.Fatal("fake parser returns EOF", err)
				}
			}()

		rescan:
			scanner := bufio.NewScanner(file)
			var i int
			for scanner.Scan() {
				if i >= *fakeCount {
					time.Sleep(1 * time.Second)
					i = 0
				}
				line := scanner.Text() + "\r\n"
				cr.Input <- line
				if *debug {
					log.Println(line)
				}
				i++
			}

			file.Seek(0, 0)
			goto rescan
		}()
	} else {
		// using the serial port
		go func() {

			for {
				dr, err := serial.OpenPort(c)
				var r io.Reader
				r = dr

				if err != nil {
					log.Println("can't open device retrying", err)
					time.Sleep(1 * time.Second)
					continue
				}
				if *logNMEA {
					pr, pw := io.Pipe()
					tee := io.TeeReader(dr, pw)
					r = pr
					logFile := "./nmea" + time.Now().Format("20060102") + ".log"
					fd, err := os.Create(logFile)
					if err != nil {
						log.Print(err.Error())
						return
					}
					go func() {
						defer fd.Close()
						if _, err := io.Copy(fd, tee); err != nil {
							log.Println(err)
						}
					}()
				}
				err = parseNMEA(r, pchan)
				if err != nil {
					log.Println("parser returns EOF retrying", err)
				}
				time.Sleep(1 * time.Second)
			}
		}()
	}

	s, err := NewServer(pchan, *osrmAddr)
	if err != nil {
		log.Fatal(err)
	}
	s.debug = *debug

	s.Start()

	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", *grpcPort))
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}

	grpcServer := grpc.NewServer()
	gpssvc.RegisterGPSSVCServer(grpcServer, s)
	grpcServer.Serve(lis)
	s.Stop()
}
