package main

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"net"
	"net/url"
	"os"
	"strings"

	"github.com/codegangsta/cli"
	"github.com/coreos/etcd/client"
	"github.com/glerchundi/etcd2-bootstrapper/util"
)

func main() {
	app := cli.NewApp()
	app.Name = "infra-helper"
	app.Version = "0.1.1"
	app.Usage = "bootstrapps etcd2 cluster"
	app.Flags = []cli.Flag {
		cli.StringFlag{
			Name: "me",
		},
		cli.StringFlag{
			Name: "members",
		},
		cli.BoolFlag{
			Name: "force, f",
		},
		cli.StringFlag{
			Name: "out, o",
			Value: "/etc/sysconfig/etcd-peers",
			Usage: "etcd peers environment file destination",
		},


	}
	app.Action = handleMain
	app.RunAndExitOnError()
}

func handleMain(c *cli.Context) {
	// get in parameters
	me := c.GlobalString("me")
	members := c.GlobalString("members")
	force := c.GlobalBool("force")
	environmentFilePath := c.GlobalString("out")

	// sanity checks
	if me == "" {
		log.Fatal(fmt.Errorf("-me is a required parameters"))
	}

	meMember, err := parseMember(me)
	if err != nil {
		log.Fatal(err)
	}

	if members == "" {
		log.Fatal(fmt.Errorf("-members is a required parameters"))
	}

	clusterMembers, err := parseMembers(members)
	if err != nil {
		log.Fatal(err)
	}

	if _, err := os.Stat(environmentFilePath); err == nil {
		log.Printf("etcd-peers file %s already created, exiting.\n", environmentFilePath)
		return
	}

	tempFilePath := environmentFilePath + ".tmp"
	tempFile, err := os.Create(tempFilePath)
	if err != nil {
		log.Fatal(err)
	}

	isClosed := false
	defer func() { if !isClosed { tempFile.Close() } }()

	if err := writeEnvironment(*meMember, clusterMembers, force, tempFile); err != nil {
		log.Fatal(err)
	}

	if err := tempFile.Close(); err != nil {
		log.Fatal(err)
	}

	isClosed = true

	if err := os.Rename(tempFilePath, environmentFilePath); err != nil {
		log.Fatal(err)
	}

	return
}

func writeEnvironment(me member, members []member, force bool, w io.Writer) error {
	var buffer bytes.Buffer
	var err error

	// retrieve current cluster members
	var etcdMembers []client.Member
	var goodEtcdClientURL string = ""
	for _, member := range members {
		// what was first, chicken or egg?
		if member.Address == me.Address {
			continue
		}

		etcdClientURL := util.EtcdClientURLFromIP(member.Address)
		etcdMembers, err = util.EtcdListMembers(etcdClientURL)
		if err == nil {
			goodEtcdClientURL = etcdClientURL
			break
		}
	}

	// etcd parameters
	var initialClusterState string
	var initialCluster string

	// check if instanceId is already member of cluster
	var isMember bool = false
	for _, member := range etcdMembers {
		if member.Name == me.Name {
			isMember = true
			break
		}
	}

	// cosmetic parameter which defines if it should join to an existing or create a new cluster
	joinExistingCluster := etcdMembers != nil && (!isMember || force)

	// if i am not already listed as a member of the cluster assume that this is a new cluster
	if joinExistingCluster {
		log.Printf("joining to an existing cluster, using this client url: %s\n", goodEtcdClientURL)

		//
		// detect and remove bad peers
		//

		// create a reverse cluster members map
		clusterMembersByIp := make(map[string]string)
		for _, member := range members {
			clusterMembersByIp[member.Address] = member.Name
		}

		for _, etcdMember := range etcdMembers {
			peerURL, err := url.Parse(etcdMember.PeerURLs[0])
			if err != nil {
				return err
			}

			peerHost, _, err := net.SplitHostPort(peerURL.Host)
			if err != nil {
				return err
			}

			if _, ok := clusterMembersByIp[peerHost]; !ok {
				log.Printf("removing etcd member: %s...", etcdMember.ID)
				err = util.EtcdRemoveMember(goodEtcdClientURL, etcdMember.ID)
				if err != nil {
					return err
				}
				log.Printf("done\n")
			}
		}

		//
		// list current etcd members (after removing spurious ones)
		//

		etcdMembers, err = util.EtcdListMembers(goodEtcdClientURL)
		if err != nil {
			return err
		}

		kvs := make([]string, 0)
		for _, etcdMember := range etcdMembers {
			// ignore unstarted peers
			if len(etcdMember.Name) == 0 {
				continue
			}
			kvs = append(kvs, fmt.Sprintf("%s=%s", etcdMember.Name, etcdMember.PeerURLs[0]))
		}
		kvs = append(kvs, fmt.Sprintf("%s=%s", me.Name, util.EtcdPeerURLFromIP(me.Address)))

		initialClusterState = "existing"
		initialCluster = strings.Join(kvs, ",")

		//
		// join an existing cluster
		//

		isAddRequired := true
		for _, etcdMember := range etcdMembers {
			if etcdMember.Name == me.Name {
				isAddRequired = false
				break
			}
		}

		if isAddRequired {
			instancePeerURL := util.EtcdPeerURLFromIP(me.Address)
			log.Printf("adding etcd member: %s...", instancePeerURL)
			member, err := util.EtcdAddMember(goodEtcdClientURL, instancePeerURL)
			if member == nil {
				return err
			}
		}

		log.Printf("done\n")
	} else {
		log.Printf("creating new cluster\n")

		// initial cluster
		kvs := make([]string, 0)
		for _, member := range members {
			kvs = append(kvs, fmt.Sprintf("%s=%s", member.Name, util.EtcdPeerURLFromIP(member.Address)))
		}

		initialClusterState = "new"
		initialCluster = strings.Join(kvs, ",")
	}

	// indicate it's going to write envvars
	log.Printf("writing environment variables...")

	// create environment variables
	buffer.WriteString(fmt.Sprintf("ETCD_NAME=%s\n", me.Name))
	buffer.WriteString(fmt.Sprintf("ETCD_INITIAL_CLUSTER_STATE=%s\n", initialClusterState))
	buffer.WriteString(fmt.Sprintf("ETCD_INITIAL_CLUSTER=%s\n", initialCluster))

	if _, err := buffer.WriteTo(w); err != nil {
		return err
	}

	// write done
	log.Printf("done\n")

	return nil
}

type member struct {
	Name string
	Address string
}

func parseMember(value string) (*member, error) {
	parts := strings.Split(value, "=")
	if len(parts) != 2 {
		return nil, fmt.Errorf("%s member doesn't follow name=ip/host format")
	}
	return &member{parts[0], parts[1]}, nil
}

func parseMembers(value string) ([]member, error) {
	list := make([]member, 0)
	parts := strings.Split(value, ",")
	for _, member := range parts {
		m, err := parseMember(member)
		if err != nil {
			return nil, err
		}
		list = append(list, *m)
	}
	return list, nil
}