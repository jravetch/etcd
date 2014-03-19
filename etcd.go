/*
Copyright 2013 CoreOS Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

     http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/coreos/etcd/third_party/github.com/coreos/raft"

	"github.com/coreos/etcd/config"
	ehttp "github.com/coreos/etcd/http"
	"github.com/coreos/etcd/log"
	"github.com/coreos/etcd/metrics"
	"github.com/coreos/etcd/server"
	"github.com/coreos/etcd/store"
)

func main() {
	// Load configuration.
	var config = config.New()
	if err := config.Load(os.Args[1:]); err != nil {
		fmt.Println(server.Usage() + "\n")
		fmt.Println(err.Error() + "\n")
		os.Exit(1)
	} else if config.ShowVersion {
		fmt.Println("etcd version", server.ReleaseVersion)
		os.Exit(0)
	} else if config.ShowHelp {
		fmt.Println(server.Usage() + "\n")
		os.Exit(0)
	}

	// Enable options.
	if config.VeryVeryVerbose {
		log.Verbose = true
		raft.SetLogLevel(raft.Trace)
	} else if config.VeryVerbose {
		log.Verbose = true
		raft.SetLogLevel(raft.Debug)
	} else if config.Verbose {
		log.Verbose = true
	}
	if config.CPUProfileFile != "" {
		profile(config.CPUProfileFile)
	}

	if config.DataDir == "" {
		log.Fatal("The data dir was not set and could not be guessed from machine name")
	}

	// Create data directory if it doesn't already exist.
	if err := os.MkdirAll(config.DataDir, 0744); err != nil {
		log.Fatalf("Unable to create path: %s", err)
	}

	// Warn people if they have an info file
	info := filepath.Join(config.DataDir, "info")
	if _, err := os.Stat(info); err == nil {
		log.Warnf("All cached configuration is now ignored. The file %s can be removed.", info)
	}

	var mbName string
	if config.Trace() {
		mbName = config.MetricsBucketName()
		runtime.SetBlockProfileRate(1)
	}

	mb := metrics.NewBucket(mbName)

	if config.GraphiteHost != "" {
		err := mb.Publish(config.GraphiteHost)
		if err != nil {
			panic(err)
		}
	}

	// Retrieve CORS configuration
	corsInfo, err := ehttp.NewCORSInfo(config.CorsOrigins)
	if err != nil {
		log.Fatal("CORS:", err)
	}

	// Create etcd key-value store and registry.
	store := store.New()
	registry := server.NewRegistry(store)

	// Create stats objects
	followersStats := server.NewRaftFollowersStats(config.Name)
	serverStats := server.NewRaftServerStats(config.Name)

	// Calculate all of our timeouts
	heartbeatInterval := time.Duration(config.Peer.HeartbeatInterval) * time.Millisecond
	electionTimeout := time.Duration(config.Peer.ElectionTimeout) * time.Millisecond
	dialTimeout := (3 * heartbeatInterval) + electionTimeout
	responseHeaderTimeout := (3 * heartbeatInterval) + electionTimeout

	// Create peer server
	psConfig := server.PeerServerConfig{
		Name:           config.Name,
		Scheme:         config.PeerTLSInfo().Scheme(),
		URL:            config.Peer.Addr,
		SnapshotCount:  config.SnapshotCount,
		MaxClusterSize: config.MaxClusterSize,
		RetryTimes:     config.MaxRetryAttempts,
		RetryInterval:  config.RetryInterval,
	}
	ps := server.NewPeerServer(psConfig, registry, store, &mb, followersStats, serverStats)

	// Create raft transporter and server
	raftTransporter := server.NewTransporter(followersStats, serverStats, registry, heartbeatInterval, dialTimeout, responseHeaderTimeout)
	if psConfig.Scheme == "https" {
		raftClientTLSConfig, err := config.PeerTLSInfo().ClientConfig()
		if err != nil {
			log.Fatal("raft client TLS error: ", err)
		}
		raftTransporter.SetTLSConfig(*raftClientTLSConfig)
	}
	raftServer, err := raft.NewServer(config.Name, config.DataDir, raftTransporter, store, ps, "")
	if err != nil {
		log.Fatal(err)
	}
	raftServer.SetElectionTimeout(electionTimeout)
	raftServer.SetHeartbeatInterval(heartbeatInterval)
	ps.SetRaftServer(raftServer)

	// Create etcd server
	s := server.New(config.Name, config.Addr, ps, registry, store, &mb)

	if config.Trace() {
		s.EnableTracing()
	}

	ps.SetServer(s)
	ps.Start(config.Snapshot, config.Discovery, config.Peers)

	go func() {
		log.Infof("peer server [name %s, listen on %s, advertised url %s]", ps.Config.Name, config.Peer.BindAddr, ps.Config.URL)
		l := server.NewListener(psConfig.Scheme, config.Peer.BindAddr, config.PeerTLSInfo())

		sHTTP := &ehttp.CORSHandler{ps.HTTPHandler(), corsInfo}
		log.Fatal(http.Serve(l, sHTTP))
	}()

	log.Infof("etcd server [name %s, listen on %s, advertised url %s]", s.Name, config.BindAddr, s.URL())
	l := server.NewListener(config.EtcdTLSInfo().Scheme(), config.BindAddr, config.EtcdTLSInfo())
	sHTTP := &ehttp.CORSHandler{s.HTTPHandler(), corsInfo}
	log.Fatal(http.Serve(l, sHTTP))
}
