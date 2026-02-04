package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"log"

	"github.com/kardianos/service"
)

// program implements service.Interface
type program struct {
	listener net.Listener
	ctx      context.Context
	cancel   context.CancelFunc
	wg       sync.WaitGroup
	connSem  chan struct{}
}

const version = "1.1.1"

var (
	logFile    *os.File
	config     *tConfig
	configFile string
	logger     *slog.Logger
	svcFlag    = flag.String("service", "", "Control the system service (start, stop, install, uninstall)")
)

func (p *program) Start(s service.Service) error {
	// Start should not block. Do the actual work async.
	p.ctx, p.cancel = context.WithCancel(context.Background())
	p.connSem = make(chan struct{}, config.MaxConnections)
	go p.run()
	return nil
}

func (p *program) run() {
	var err error
	p.listener, err = net.Listen("tcp", config.ListenAddr)
	if err != nil {
		logger.Error("Failed to listen", "error", err)
		return
	}

	logger.Info("SMTP relay listening", "address", config.ListenAddr, "max_connections", config.MaxConnections)

	// Start token cache cleanup
	StartTokenCacheCleanup(p.ctx, 5*time.Minute)

	for {
		// Set accept deadline to check for shutdown periodically
		if tcpListener, ok := p.listener.(*net.TCPListener); ok {
			tcpListener.SetDeadline(time.Now().Add(1 * time.Second))
		}

		conn, err := p.listener.Accept()
		if err != nil {
			if opErr, ok := err.(*net.OpError); ok && opErr.Timeout() {
				// Check if we're shutting down
				select {
				case <-p.ctx.Done():
					logger.Info("Shutdown signal received, stopping accept loop")
					return
				default:
					continue
				}
			}
			// Check for shutdown before logging error
			select {
			case <-p.ctx.Done():
				return
			default:
				logger.Error("Accept error", "error", err)
				continue
			}
		}

		// Try to acquire semaphore (non-blocking)
		select {
		case p.connSem <- struct{}{}:
			p.wg.Add(1)
			go func() {
				defer p.wg.Done()
				defer func() { <-p.connSem }()
				handleSMTPConnection(conn)
			}()
		case <-p.ctx.Done():
			conn.Close()
			return
		default:
			// At capacity - reject connection
			conn.Write([]byte("421 4.7.0 Too many connections, try again later\r\n"))
			conn.Close()
			logger.Warn("Connection rejected: at capacity", "max", config.MaxConnections, "remote", conn.RemoteAddr())
		}
	}
}

func (p *program) Stop(s service.Service) error {
	logger.Info("Service stopping, initiating graceful shutdown...")

	// Signal shutdown
	if p.cancel != nil {
		p.cancel()
	}

	// Close listener to stop accepting new connections
	if p.listener != nil {
		p.listener.Close()
	}

	// Wait for existing connections with timeout
	done := make(chan struct{})
	go func() {
		p.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		logger.Info("All connections closed gracefully")
	case <-time.After(30 * time.Second):
		logger.Warn("Shutdown timeout (30s), some connections may not have completed")
	}

	// Close log file
	if logFile != nil && logFile != os.Stdout {
		logFile.Close()
	}

	return nil
}

func main() {
	configFile = filepath.Join(filepath.Dir(os.Args[0]), "config.yaml")
	if err := loadConfig(); err != nil {
		log.Fatalf("failed to load config: %v", err)
	}
	if err := slogSetup(); err != nil {
		log.Fatalf("failed to initialize logger: %v", err)
	}
	flagsProcess()

	logger.Info("azureSMTPwithOAuth (systems@work) Github: https://github.com/mmalcek/azureSMTPwithOAuth")
	logger.Info("Starting Service", "version", version)

	prg := &program{}
	svcConfig := &service.Config{
		Name:        "azureSMTPwithOAuth",
		DisplayName: "azureSMTPwithOAuth",
		Description: "azureSMTPwithOAuth (systems@work) is a service that provides SMTP functionality with OAuth authentication through the Microsoft Graph API. https://github.com/mmalcek/azureSMTPwithOAuth",
	}

	s, err := service.New(prg, svcConfig)
	if err != nil {
		logger.Error("service.New failed", "err", err)
		os.Exit(1)
	}

	// If -service flag is set, control the service (install, start, stop, uninstall)
	if *svcFlag != "" {
		err := service.Control(s, *svcFlag)
		if err != nil {
			log.Fatal("service.Control error: ", err)
		}
		switch *svcFlag {
		case "install":
			fmt.Println("Service installed successfully")
		case "uninstall":
			fmt.Println("Service uninstalled successfully")
		case "start":
			fmt.Println("Service started successfully")
		case "stop":
			fmt.Println("Service stopped successfully")
		}
		return
	}

	err = s.Run()
	if err != nil {
		logger.Error("service.Run failed", "err", err)
		os.Exit(1)
	}
}
