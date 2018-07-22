package main

type ChanReader struct {
	Input chan (string)
}

func NewChanReader() *ChanReader {
	return &ChanReader{
		Input: make(chan (string), 1),
	}
}

func (r *ChanReader) Read(p []byte) (n int, e error) {
	for {
		data := <-r.Input
		for i, c := range []byte(data) {
			p[i] = c
		}

		return len(data), nil
	}
	return 0, nil
}
