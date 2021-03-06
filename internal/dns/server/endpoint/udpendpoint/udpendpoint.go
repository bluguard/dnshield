package udpendpoint

import (
	"context"
	"log"
	"net"
	"sync"
	"time"

	"github.com/bluguard/dnshield/internal/dns/dto"
	"github.com/bluguard/dnshield/internal/dns/resolver"
	"github.com/bluguard/dnshield/internal/dns/server/endpoint"
)

var _ endpoint.Endpoint = &UdpEndpoint{}

type response struct {
	message     dto.Message
	destination net.UDPAddr
}

func NewUdpEndpoint(address string, chain *resolver.ResolverChain) *UdpEndpoint {
	return &UdpEndpoint{
		laddr:    address,
		chain:    chain,
		lock:     sync.RWMutex{},
		started:  false,
		sendChan: make(chan response),
	}
}

type UdpEndpoint struct {
	laddr    string
	chain    *resolver.ResolverChain
	lock     sync.RWMutex
	started  bool
	sendChan chan response
}

// SetChain implements server.Endpoint
func (e *UdpEndpoint) SetChain(chain *resolver.ResolverChain) {
	e.lock.Lock()
	defer e.lock.Unlock()
	e.chain = chain
}

// Start implements server.Endpoint
func (e *UdpEndpoint) Start(ctx context.Context, wg *sync.WaitGroup) {
	if e.started {
		panic("endpoint is already started")
	}
	log.Println("starting udp endpoint on ", e.laddr)
	e.started = true
	go e.run(ctx, wg)
}

func (e *UdpEndpoint) run(ctx context.Context, ewg *sync.WaitGroup) {
	defer ewg.Done()
	address, err := net.ResolveUDPAddr("udp", e.laddr)
	if err != nil {
		log.Println(err)
		return
	}
	udpConn, err := net.ListenUDP("udp", address)
	if err != nil {
		log.Println(err)
		return
	}
	udpConn.SetReadBuffer(dto.BufferMaxLength)
	defer udpConn.Close()
	iwg := sync.WaitGroup{}

	for i := 0; i < 10; i++ {
		iwg.Add(1)
		go e.receivingLoop(ctx, udpConn, &iwg)
	}
	iwg.Add(1)
	go e.sendingLoop(ctx, udpConn, &iwg)

	iwg.Wait()
	log.Println("udp endpoint on ", e.laddr, "stopped")
}

func (e *UdpEndpoint) receivingLoop(ctx context.Context, udpConn *net.UDPConn, wg *sync.WaitGroup) {
	//Main loop
	defer wg.Done()
	defer udpConn.Close()
	for {
		start := time.Now()
		select {
		case <-ctx.Done():
			log.Println("udp endpoint on ", e.laddr, " is terminating")
			return
		default:
			buffer := make([]byte, dto.BufferMaxLength)
			err := udpConn.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
			if err != nil {
				log.Println(err)
				return
			}
			n, addr, err := udpConn.ReadFromUDP(buffer)
			if err != nil {
				if terr, ok := err.(net.Error); ok && terr.Timeout() { // if timeout loop
					continue
				} else {
					log.Println(err)
					return
				}
			}
			data := buffer[0:n]
			go e.handleRequest(data, addr)
			log.Println("receiving loop iteration took", time.Since(start))
		}
	}
}

func (e *UdpEndpoint) sendingLoop(ctx context.Context, udpConn *net.UDPConn, iwg *sync.WaitGroup) {
	defer iwg.Done()
	defer udpConn.Close()
	for {
		select {
		case <-ctx.Done():
			return
		case resp := <-e.sendChan:
			payload := dto.SerializeMessage(resp.message)
			err := udpConn.SetWriteDeadline(time.Now().Add(200 * time.Millisecond))
			if err != nil {
				log.Println(err)
				return
			}
			_, err = udpConn.WriteToUDP(payload, &resp.destination)
			if err != nil {
				if terr, ok := err.(net.Error); ok && terr.Timeout() { // if timeout loop
					continue
				} else {
					log.Println(err)
					return
				}
			}
		}
	}
}

func (e *UdpEndpoint) handleRequest(buffer []byte, addr *net.UDPAddr) {
	//log.Println("Handling request for ", addr.IP)
	start := time.Now()
	e.lock.RLock()
	defer e.lock.RUnlock()
	message, err := dto.ParseMessage(buffer)
	if err != nil {
		log.Println(err)
		return
	}
	res := e.chain.Resolve(*message)
	//log.Println("Handling request for ", addr.IP, message, " -> ", res)
	e.sendChan <- response{
		message:     res,
		destination: *addr,
	}
	delay := time.Since(start)
	log.Println("resolving", message.QuestionCount, "questions took", delay.String())
}
