# gpsd
A gps daemon with a gRPC API

Used for my own car system project see [this blogpost](https://blog.nobugware.com/post/2018/my_own_car_system_raspberry_pi_offline_mapping/)

## Usage
```
Usage of gpsd:
  -debug
        enable debug
  -device string
        Device path (default "/dev/ttyACM0")
  -fakeCount int
        how many fake NMEA lines per sequences (default 3)
  -fakePath string
        fake NMEA data file for debug
  -grpcPort int
        grpc port to listen (default 9402)
  -httpPort int
        http port to listen (default 9401)
  -logNMEA
        log all NMEA output in current directory
  -osmrAddr string
        OSRM API address (default "http://localhost:5000")
  -speed int
        Speed in bauds (default 38400)
  -timeout duration
        Timeout in second (default 4s)
```

## API
A gRPC API is exposed for a client to retrieve the positions updates:
```
rpc LivePosition(google.protobuf.Empty) returns (stream Position);
```

Look in `cmd/gpsc` for an example client.