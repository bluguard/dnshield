package server

import (
	"context"
	"log"
	"os"
	"os/signal"
	"runtime/pprof"
	"sync"
	"syscall"
	"time"

	"github.com/bluguard/dnshield/internal/dns/cache/memorycache"
	"github.com/bluguard/dnshield/internal/dns/client"
	"github.com/bluguard/dnshield/internal/dns/client/blocker"
	"github.com/bluguard/dnshield/internal/dns/client/doh"
	inmemoryclient "github.com/bluguard/dnshield/internal/dns/client/inMemoryClient"
	"github.com/bluguard/dnshield/internal/dns/client/udp"
	"github.com/bluguard/dnshield/internal/dns/resolver"
	"github.com/bluguard/dnshield/internal/dns/server/configuration"
	"github.com/bluguard/dnshield/internal/dns/server/endpoint"
	"github.com/bluguard/dnshield/internal/dns/server/endpoint/udpendpoint"
	blockparser "github.com/bluguard/dnshield/internal/dns/util/blockParser"
)

type Server struct {
	chain     resolver.ResolverChain
	endpoints []endpoint.Endpoint
	started   bool
	//http controller
	cancelFunc context.CancelFunc
}

func (s *Server) Start(conf configuration.ServerConf) *sync.WaitGroup {
	if s.started {
		log.Println("server already started")
	}
	log.Println("starting server ...")
	s.started = true

	ch := make(chan os.Signal, 1)

	signal.Notify(ch, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-ch
		if conf.Memdump != "" {
			memDump(conf.Memdump)
		}

		if s.cancelFunc != nil {
			s.cancelFunc()
		}
	}()

	wg := s.Reconfigure(conf)
	log.Println("server started")
	return wg

}

func (s *Server) Stop() {
	if s.cancelFunc != nil {
		s.cancelFunc()
	}
}

func (s *Server) Reconfigure(conf configuration.ServerConf) *sync.WaitGroup {
	if s.cancelFunc != nil {
		s.cancelFunc()
	}

	ctx, cancelFunc := context.WithCancel(context.Background())
	s.cancelFunc = cancelFunc

	wg := sync.WaitGroup{}

	cache := memorycache.NewMemoryCache(ctx, &wg, conf.Cache.Size, conf.Cache.Basettl, conf.Cache.ForceBasettl, 1*time.Minute)

	blocker, initBlocker := buildBlocker(conf)

	s.chain = *resolver.NewResolverChain([]resolver.Resolver{
		resolver.NewClientresolver(blocker, "Block"),
		resolver.NewClientresolver(buildCustom(conf), "Custom"),
		resolver.NewClientresolver(cache, "Cache"),
		resolver.NewCacheFeeder(resolver.NewClientresolver(buildExternal(conf), "External"), cache),
	})

	s.endpoints = createEndpoints(conf, &s.chain)

	for _, endpoint := range s.endpoints {
		wg.Add(1)
		endpoint.Start(ctx, &wg)
	}
	initBlocker()
	return &wg
}

func createEndpoints(conf configuration.ServerConf, chain *resolver.ResolverChain) []endpoint.Endpoint {
	return []endpoint.Endpoint{
		udpendpoint.NewUDPEndpoint(conf.Endpoint.Address, chain),
	}
}

func buildExternal(conf configuration.ServerConf) client.Client {
	if !conf.AllowExternal {
		panic("unexpected")
	}
	switch conf.External.Type {
	case "DOH":
		return doh.NewDOHClient(conf.External.Endpoint)
	default:
		return udp.NewUDPClient(conf.External.Endpoint)
	}
}

func buildCustom(conf configuration.ServerConf) client.Client {
	res := inmemoryclient.InMemoryClient{}
	for _, v := range conf.Custom {
		err := res.Add(v.Name, v.Address)
		if err != nil {
			log.Println("error creating inmemory source ", err)
		}
	}

	return &res
}

func buildBlocker(conf configuration.ServerConf) (client.Client, func()) {
	res := make(blocker.Blocker, 10000)
	return &res, func() {
		go func() {
			for _, url := range conf.BlockingLists {
				parser := blockparser.BlockParser{Url: url}
				res.Init(parser.Feed)
			}
		}()
	}
}

//The optimal chain is
// Client(Blocker) -> Client(Memory) -> Client(Cache) -> CacheFeeder((Multiple(Client(udp/https))))

func memDump(memprofile string) {
	if memprofile != "" {
		f, err := os.Create(memprofile)
		if err != nil {
			return
		}
		_ = pprof.WriteHeapProfile(f)
		_ = f.Close()
	}
}
