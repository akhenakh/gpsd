package main

import (
	"bufio"
	"io"
	"log"

	nmea "github.com/adrianmo/go-nmea"
	"github.com/akhenakh/gpsd/gpssvc"
)

/*
$GPRMC,002503.00,A,4647.87042,N,07114.17012,W,0.065,,140718,,,D*65
$GPVTG,,T,,M,0.065,N,0.121,K,D*27
$GPGGA,002503.00,4647.87042,N,07114.17012,W,2,11,0.84,76.3,M,-30.0,M,,0000*57
$GPGSA,A,3,13,05,29,15,51,21,02,30,07,20,48,,1.50,0.84,1.24*0A
$GPGSV,4,1,13,02,20,136,18,05,46,073,38,07,08,031,18,13,70,117,34*7A
$GPGSV,4,2,13,15,52,203,33,16,03,320,,20,09,271,38,21,34,305,34*7B
$GPGSV,4,3,13,29,41,230,45,30,20,057,32,46,13,245,36,48,10,249,31*76
$GPGSV,4,4,13,51,26,225,38*45
$GPGLL,4647.87042,N,07114.17012,W,002503.00,A,D*74
*/

// parseNMEA parse NMEA lines close the channel on EOF
func parseNMEA(r io.Reader, pchan chan *gpssvc.Position) error {
	sc := bufio.NewScanner(r)
	pos := &gpssvc.Position{}
	for sc.Scan() {
		line := sc.Text()
		s, err := nmea.Parse(line)
		if err != nil {
			log.Println("error parsing", err, line)
			continue
		}

		switch v := s.(type) {
		case nmea.GPVTG:
			pos.Speed = v.GroundSpeedKPH
		case nmea.GPRMC:
			pos.Heading = v.Course
			if pos.Speed == 0 {
				pos.Speed = v.Speed * 1.852001
			}
			if *adjustTime {
				setTime(v)
			}
		case nmea.GPGGA:
			pos.Latitude = v.Latitude
			pos.Longitude = v.Longitude
			// TODO: compute HorizontalPrecision
			pos.HorizontalPrecision = v.HDOP
			if *debug {
				log.Println(pos)
			}

			// on some GPS GPGGA is the latest the order
			// can't find any docs about sentences ordering ..
			if pos.Latitude != 0.0 && pos.Longitude != 0.0 {
				pchan <- pos
			}
			pos = &gpssvc.Position{}
		}
	}

	return sc.Err()
}
