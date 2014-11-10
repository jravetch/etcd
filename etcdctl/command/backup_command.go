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

package command

import (
	"log"
	"math/rand"
	"path"
	"time"

	"github.com/coreos/etcd/Godeps/_workspace/src/github.com/codegangsta/cli"
	"github.com/coreos/etcd/etcdserver"
	"github.com/coreos/etcd/etcdserver/etcdserverpb"
	"github.com/coreos/etcd/pkg/pbutil"
	"github.com/coreos/etcd/snap"
	"github.com/coreos/etcd/wal"
)

func NewBackupCommand() cli.Command {
	return cli.Command{
		Name:  "backup",
		Usage: "backup an etcd directory",
		Flags: []cli.Flag{
			cli.StringFlag{Name: "data-dir", Value: "", Usage: "Path to the etcd data dir"},
			cli.StringFlag{Name: "backup-dir", Value: "", Usage: "Path to the backup dir"},
		},
		Action: handleBackup,
	}
}

// handleBackup handles a request that intends to do a backup.
func handleBackup(c *cli.Context) {
	srcSnap := path.Join(c.String("data-dir"), "snap")
	destSnap := path.Join(c.String("backup-dir"), "snap")
	srcWAL := path.Join(c.String("data-dir"), "wal")
	destWAL := path.Join(c.String("backup-dir"), "wal")

	ss := snap.New(srcSnap)
	snapshot, err := ss.Load()
	if err != nil && err != snap.ErrNoSnapshot {
		log.Fatal(err)
	}
	var index uint64
	if snapshot != nil {
		index = snapshot.Index
		newss := snap.New(destSnap)
		newss.SaveSnap(*snapshot)
	}

	w, err := wal.OpenAtIndex(srcWAL, index)
	if err != nil {
		log.Fatal(err)
	}
	defer w.Close()
	wmetadata, state, ents, err := w.ReadAll()
	if err != nil {
		log.Fatal(err)
	}
	var metadata etcdserverpb.Metadata
	pbutil.MustUnmarshal(&metadata, wmetadata)
	rand.Seed(time.Now().UnixNano())
	metadata.NodeID = etcdserver.GenID()
	metadata.ClusterID = etcdserver.GenID()

	neww, err := wal.Create(destWAL, pbutil.MustMarshal(&metadata))
	if err != nil {
		log.Fatal(err)
	}
	defer neww.Close()
	if err := neww.Save(state, ents); err != nil {
		log.Fatal(err)
	}
}
