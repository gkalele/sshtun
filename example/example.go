package main

import (
	"context"
	"log"
	"time"

	"github.com/gkalele/sshtun"
)

func main() {
	// We want to connect to port 8080 on our machine to acces port 80 on my.super.host.com
	sshTun := sshtun.New(8080, "my.super.host.com", 80)
	sshTun.SetName("Test")
	// We print each tunneled state to see the connections status
	sshTun.SetTunneledConnState(func(tun *sshtun.SSHTun, state *sshtun.TunneledConnState) {
		log.Printf("%s %+v", tun.Name(), state)
	})

	// We set a callback to know when the tunnel is ready
	sshTun.SetConnState(func(tun *sshtun.SSHTun, state sshtun.ConnState) {
		switch state {
		case sshtun.StateStarting:
			log.Printf("%s STATE is Starting", tun.Name())
		case sshtun.StateStarted:
			log.Printf("%s STATE is Started", tun.Name())
		case sshtun.StateStopped:
			log.Printf("%s STATE is Stopped", tun.Name())
		}
	})

	// We start the tunnel (and restart it every time it is stopped)
	go func() {
		for {
			if err := sshTun.Start(context.Background()); err != nil {
				log.Printf("SSH tunnel error: %v", err)
				time.Sleep(time.Second) // don't flood if there's a start error :)
			}
		}
	}()

	// We stop the tunnel every 20 seconds (just to see what happens)
	for {
		time.Sleep(time.Second * time.Duration(20))
		log.Println("Lets stop the SSH tunnel...")
		sshTun.Stop()
	}
}
