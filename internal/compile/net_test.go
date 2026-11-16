package compile

import "net"

type netTCPAddr = net.TCPAddr

func netListenLocal() (net.Listener, error) {
	return net.Listen("tcp", "127.0.0.1:0")
}
