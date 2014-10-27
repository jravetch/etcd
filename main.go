/*
   Copyright 2014 CoreOS, Inc.

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
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/coreos/etcd/etcdserver"
	"github.com/coreos/etcd/etcdserver/etcdhttp"
	"github.com/coreos/etcd/pkg"
	flagtypes "github.com/coreos/etcd/pkg/flags"
	"github.com/coreos/etcd/pkg/transport"
	"github.com/coreos/etcd/pkg/types"
	"github.com/coreos/etcd/proxy"
	"github.com/coreos/etcd/version"
)

const (
	// the owner can make/remove files inside the directory
	privateDirMode = 0700
)

var (
	fs           = flag.NewFlagSet("etcd", flag.ContinueOnError)
	name         = fs.String("name", "default", "Unique human-readable name for this node")
	dir          = fs.String("data-dir", "", "Path to the data directory")
	durl         = fs.String("discovery", "", "Discovery service used to bootstrap the cluster")
	snapCount    = fs.Uint64("snapshot-count", etcdserver.DefaultSnapCount, "Number of committed transactions to trigger a snapshot")
	printVersion = fs.Bool("version", false, "Print the version and exit")

	initialCluster     = fs.String("initial-cluster", "default=http://localhost:2380,default=http://localhost:7001", "Initial cluster configuration for bootstrapping")
	initialClusterName = fs.String("initial-cluster-name", "etcd", "Initial name for the etcd cluster during bootstrap")
	clusterState       = new(etcdserver.ClusterState)

	cors      = &pkg.CORSInfo{}
	proxyFlag = new(flagtypes.Proxy)

	clientTLSInfo = transport.TLSInfo{}
	peerTLSInfo   = transport.TLSInfo{}

	ignored = []string{
		"cluster-active-size",
		"cluster-remove-delay",
		"cluster-sync-interval",
		"config",
		"force",
		"max-result-buffer",
		"max-retry-attempts",
		"peer-heartbeat-interval",
		"peer-election-timeout",
		"retry-interval",
		"snapshot",
		"v",
		"vv",
	}
)

func init() {
	fs.Var(clusterState, "initial-cluster-state", "Initial cluster configuration for bootstrapping")
	clusterState.Set(etcdserver.ClusterStateValueNew)

	fs.Var(flagtypes.NewURLsValue("http://localhost:2380,http://localhost:7001"), "initial-advertise-peer-urls", "List of this member's peer URLs to advertise to the rest of the cluster")
	fs.Var(flagtypes.NewURLsValue("http://localhost:2379,http://localhost:4001"), "advertise-client-urls", "List of this member's client URLs to advertise to the rest of the cluster")
	fs.Var(flagtypes.NewURLsValue("http://localhost:2380,http://localhost:7001"), "listen-peer-urls", "List of URLs to listen on for peer traffic")
	fs.Var(flagtypes.NewURLsValue("http://localhost:2379,http://localhost:4001"), "listen-client-urls", "List of URLs to listen on for client traffic")

	fs.Var(cors, "cors", "Comma-separated white list of origins for CORS (cross-origin resource sharing).")

	fs.Var(proxyFlag, "proxy", fmt.Sprintf("Valid values include %s", strings.Join(flagtypes.ProxyValues, ", ")))
	proxyFlag.Set(flagtypes.ProxyValueOff)

	fs.StringVar(&clientTLSInfo.CAFile, "ca-file", "", "Path to the client server TLS CA file.")
	fs.StringVar(&clientTLSInfo.CertFile, "cert-file", "", "Path to the client server TLS cert file.")
	fs.StringVar(&clientTLSInfo.KeyFile, "key-file", "", "Path to the client server TLS key file.")

	fs.StringVar(&peerTLSInfo.CAFile, "peer-ca-file", "", "Path to the peer server TLS CA file.")
	fs.StringVar(&peerTLSInfo.CertFile, "peer-cert-file", "", "Path to the peer server TLS cert file.")
	fs.StringVar(&peerTLSInfo.KeyFile, "peer-key-file", "", "Path to the peer server TLS key file.")

	// backwards-compatibility with v0.4.6
	fs.Var(&flagtypes.IPAddressPort{}, "addr", "DEPRECATED: Use -advertise-client-urls instead.")
	fs.Var(&flagtypes.IPAddressPort{}, "bind-addr", "DEPRECATED: Use -listen-client-urls instead.")
	fs.Var(&flagtypes.IPAddressPort{}, "peer-addr", "DEPRECATED: Use -initial-advertise-peer-urls instead.")
	fs.Var(&flagtypes.IPAddressPort{}, "peer-bind-addr", "DEPRECATED: Use -listen-peer-urls instead.")

	for _, f := range ignored {
		fs.Var(&pkg.IgnoredFlag{Name: f}, f, "")
	}

	fs.Var(&pkg.DeprecatedFlag{Name: "peers"}, "peers", "DEPRECATED: Use -initial-cluster instead")
	fs.Var(&pkg.DeprecatedFlag{Name: "peers-file"}, "peers-file", "DEPRECATED: Use -initial-cluster instead")
}

func main() {
	fs.Usage = pkg.UsageWithIgnoredFlagsFunc(fs, ignored)
	err := fs.Parse(os.Args[1:])
	switch err {
	case nil:
	case flag.ErrHelp:
		os.Exit(0)
	default:
		os.Exit(2)
	}

	if *printVersion {
		fmt.Println("etcd version", version.Version)
		os.Exit(0)
	}

	pkg.SetFlagsFromEnv(fs)

	if string(*proxyFlag) == flagtypes.ProxyValueOff {
		startEtcd()
	} else {
		startProxy()
	}

	// Block indefinitely
	<-make(chan struct{})
}

// startEtcd launches the etcd server and HTTP handlers for client/server communication.
func startEtcd() {
	cls, err := setupCluster()
	if err != nil {
		log.Fatalf("etcd: error setting up initial cluster: %v", err)
	}

	if *dir == "" {
		*dir = fmt.Sprintf("%v.etcd", *name)
		log.Printf("etcd: no data-dir provided, using default data-dir ./%s", *dir)
	}
	if err := os.MkdirAll(*dir, privateDirMode); err != nil {
		log.Fatalf("etcd: cannot create data directory: %v", err)
	}

	pt, err := transport.NewTransport(peerTLSInfo)
	if err != nil {
		log.Fatal(err)
	}

	acurls, err := pkg.URLsFromFlags(fs, "advertise-client-urls", "addr", clientTLSInfo)
	if err != nil {
		log.Fatal(err.Error())
	}
	cfg := &etcdserver.ServerConfig{
		Name:         *name,
		ClientURLs:   acurls,
		DataDir:      *dir,
		SnapCount:    *snapCount,
		Cluster:      cls,
		DiscoveryURL: *durl,
		ClusterState: *clusterState,
		Transport:    pt,
	}
	s := etcdserver.NewServer(cfg)
	s.Start()

	ch := &pkg.CORSHandler{
		Handler: etcdhttp.NewClientHandler(s),
		Info:    cors,
	}
	ph := etcdhttp.NewPeerHandler(s)

	lpurls, err := pkg.URLsFromFlags(fs, "listen-peer-urls", "peer-bind-addr", peerTLSInfo)
	if err != nil {
		log.Fatal(err.Error())
	}

	for _, u := range lpurls {
		l, err := transport.NewListener(u.Host, peerTLSInfo)
		if err != nil {
			log.Fatal(err)
		}

		// Start the peer server in a goroutine
		urlStr := u.String()
		go func() {
			log.Print("etcd: listening for peers on ", urlStr)
			log.Fatal(http.Serve(l, ph))
		}()
	}

	lcurls, err := pkg.URLsFromFlags(fs, "listen-client-urls", "bind-addr", clientTLSInfo)
	if err != nil {
		log.Fatal(err.Error())
	}

	// Start a client server goroutine for each listen address
	for _, u := range lcurls {
		l, err := transport.NewListener(u.Host, clientTLSInfo)
		if err != nil {
			log.Fatal(err)
		}

		urlStr := u.String()
		go func() {
			log.Print("etcd: listening for client requests on ", urlStr)
			log.Fatal(http.Serve(l, ch))
		}()
	}
}

// startProxy launches an HTTP proxy for client communication which proxies to other etcd nodes.
func startProxy() {
	cls, err := setupCluster()
	if err != nil {
		log.Fatalf("etcd: error setting up initial cluster: %v", err)
	}

	pt, err := transport.NewTransport(clientTLSInfo)
	if err != nil {
		log.Fatal(err)
	}

	// TODO(jonboulle): update peerURLs dynamically (i.e. when updating
	// clientURLs) instead of just using the initial fixed list here
	peerURLs := cls.PeerURLs()
	uf := func() []string {
		cls, err := etcdserver.GetClusterFromPeers(peerURLs)
		if err != nil {
			log.Printf("etcd: %v", err)
			return []string{}
		}
		return cls.ClientURLs()
	}
	ph := proxy.NewHandler(pt, uf)
	ph = &pkg.CORSHandler{
		Handler: ph,
		Info:    cors,
	}

	if string(*proxyFlag) == flagtypes.ProxyValueReadonly {
		ph = proxy.NewReadonlyHandler(ph)
	}

	lcurls, err := pkg.URLsFromFlags(fs, "listen-client-urls", "bind-addr", clientTLSInfo)
	if err != nil {
		log.Fatal(err.Error())
	}
	// Start a proxy server goroutine for each listen address
	for _, u := range lcurls {
		l, err := transport.NewListener(u.Host, clientTLSInfo)
		if err != nil {
			log.Fatal(err)
		}

		host := u.Host
		go func() {
			log.Print("etcd: proxy listening for client requests on ", host)
			log.Fatal(http.Serve(l, ph))
		}()
	}
}

// setupCluster sets up the cluster definition for bootstrap or discovery.
func setupCluster() (*etcdserver.Cluster, error) {
	set := make(map[string]bool)
	fs.Visit(func(f *flag.Flag) {
		set[f.Name] = true
	})
	if set["discovery"] && set["initial-cluster"] {
		return nil, fmt.Errorf("both discovery and bootstrap-config are set")
	}
	apurls, err := pkg.URLsFromFlags(fs, "initial-advertise-peer-urls", "addr", peerTLSInfo)
	if err != nil {
		return nil, err
	}

	var cls *etcdserver.Cluster
	switch {
	case set["discovery"]:
		clusterStr := genClusterString(*name, apurls)
		cls, err = etcdserver.NewClusterFromString(*durl, clusterStr)
	case set["initial-cluster"]:
		fallthrough
	default:
		// We're statically configured, and cluster has appropriately been set.
		// Try to configure by indexing the static cluster by name.
		cls, err = etcdserver.NewClusterFromString(*initialClusterName, *initialCluster)
	}
	return cls, err
}

func genClusterString(name string, urls types.URLs) string {
	addrs := make([]string, 0)
	for _, u := range urls {
		addrs = append(addrs, fmt.Sprintf("%v=%v", name, u.String()))
	}
	return strings.Join(addrs, ",")
}
