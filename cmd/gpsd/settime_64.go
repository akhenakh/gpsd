// +build amd64

package main

import (
	"log"
	"syscall"
	"time"

	nmea "github.com/adrianmo/go-nmea"
)

func setTime(v nmea.GPRMC) {
	// we should normally take leap seconds into account but
	// GPRMC returns leap seconds offsets without telling, after several time
	// so do not use this for extra precision
	t := time.Date(2000+v.Date.YY, time.Month(v.Date.MM), v.Date.DD, v.Time.Hour, v.Time.Minute, v.Time.Second, 0, time.UTC)
	log.Println(t)
	// if err we silent it
	syscall.Settimeofday(&syscall.Timeval{Sec: t.Unix()})
}
