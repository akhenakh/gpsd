package main

import (
	"context"
	"flag"
	"log"

	"github.com/akhenakh/gpsd/gpssvc"
	"github.com/golang/protobuf/ptypes/empty"
	"google.golang.org/grpc"
)

var (
	grpcAddr = flag.String("grpcAddr", "localhost:9402", "grpc addr to connect")
	debug    = flag.Bool("debug", false, "enable debug")
)

func main() {
	log.SetFlags(log.Lshortfile)
	flag.Parse()

	conn, err := grpc.Dial(*grpcAddr, grpc.WithInsecure())
	if err != nil {
		log.Fatal(err)
	}
	defer conn.Close()
	client := gpssvc.NewGPSSVCClient(conn)
	pos, err := client.LivePosition(context.Background(), &empty.Empty{})
	if err != nil {
		log.Fatal(err)
	}
	for {
		p, err := pos.Recv()
		if err != nil {
			log.Fatal(err)
		}
		log.Println(p)
	}
}
