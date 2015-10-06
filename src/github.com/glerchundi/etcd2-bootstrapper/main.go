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
	"text/template"

	"github.com/codegangsta/cli"
	"github.com/coreos/etcd/client"
	"github.com/glerchundi/etcd2-bootstrapper/etcd"
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
		cli.StringFlag{
			Name: "tmpl-listen-url",
		},
		cli.StringFlag{
			Name: "tmpl-peer-url",
		},
		cli.StringFlag{
			Name: "cert-file",
			Usage: "identify HTTPS client using this SSL certificate file",
		},
		cli.StringFlag{
			Name: "key-file",
			Usage: "identify HTTPS client using this SSL key file",
		},
		cli.StringFlag{
			Name: "ca-file",
			Usage: "verify certificates of HTTPS-enabled servers using this CA bundle",
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
	tmplListenUrl := c.GlobalString("tmpl-listen-url")
	tmplPeerUrl := c.GlobalString("tmpl-peer-url")
	force := c.GlobalBool("force")
	certFile := c.GlobalString("cert-file")
	keyFile := c.GlobalString("key-file")
	caCertFile := c.GlobalString("ca-file")
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

	var tmplListen *template.Template = nil
	if tmplListenUrl != "" {
		tmplListen, _ = template.New("tmpl-listen-url").Parse(tmplListenUrl)
	}

	var tmplPeer *template.Template = nil
	if tmplPeerUrl != "" {
		tmplPeer, _ = template.New("tmpl-listen-url").Parse(tmplPeerUrl)
	}

	if _, err := os.Stat(environmentFilePath); err == nil {
		log.Printf("etcd-peers file %s already created, exiting.\n", environmentFilePath)
		return
	}

	// create client instantiator

	var newClientFn func(string)(*etcd.Client,error) = nil
	if certFile != "" && keyFile != "" && caCertFile != "" {
		newClientFn = func(url string)(*etcd.Client,error) {
			return etcd.NewTLSClient(url, certFile, keyFile, caCertFile)
		}
	} else {
		newClientFn = func(url string)(*etcd.Client,error) {
			return etcd.NewClient(url)
		}
	}

	kvs, err := syncEtcdCluster(*meMember, clusterMembers, force, tmplListen, tmplPeer, newClientFn)
	if err != nil {
		log.Fatal(err)
	}

	tempFilePath := environmentFilePath + ".tmp"
	tempFile, err := os.Create(tempFilePath)
	if err != nil {
		log.Fatal(err)
	}

	isClosed := false
	defer func() { if !isClosed { tempFile.Close() } }()

	if err := writeEnvironmentFile(kvs, tempFile); err != nil {
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

func syncEtcdCluster(me member, members []member, force bool,
                     tmplListenUrl, tmplPeerUrl *template.Template,
                     newClientFn func(string)(*etcd.Client,error)) (map[string]string, error) {
	// retrieve current cluster members
	var etcdMembers []client.Member
	var goodEtcdClientURL string = ""
	for _, member := range members {
		// what was first, chicken or egg?
		if member.Address == me.Address {
			continue
		}

		etcdClientURL, err := getListenURL(member.Address, tmplListenUrl)
		if err != nil {
			return nil, err
		}

		etcdClient, err := newClientFn(etcdClientURL)
		if err != nil {
			log.Printf("unable to create etcd client for %s: %v\n", etcdClientURL, err)
			continue
		}

		etcdMembers, err = etcdClient.ListMembers()
		if err != nil {
			log.Printf("unable to list etcd members: %v\n", err)
			continue
		} else {
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

		// detect if it's required to add or not
		isAddRequired := true

		// create a reverse cluster members map
		clusterMembersByIp := make(map[string]string)
		for _, member := range members {
			clusterMembersByIp[member.Address] = member.Name
		}

		for _, etcdMember := range etcdMembers {
			peerURL, err := url.Parse(etcdMember.PeerURLs[0])
			if err != nil {
				return nil, err
			}

			peerHost, _, err := net.SplitHostPort(peerURL.Host)
			if err != nil {
				return nil, err
			}

			if peerHost == me.Address {
				isAddRequired = false
			}

			_, ok := clusterMembersByIp[peerHost]
			isRemoveRequired := !ok || (force && etcdMember.Name == me.Name)

			if isRemoveRequired {
				log.Printf("removing etcd member: %s...", etcdMember.ID)

				etcdClient, err := newClientFn(goodEtcdClientURL)
				if err != nil {
					return nil, err
				}

				err = etcdClient.RemoveMember(etcdMember.ID)
				if err != nil {
					return nil, err
				}

				log.Printf("done\n")
			}
		}

		//
		// list current etcd members (after removing spurious ones)
		//

		etcdClient, err := newClientFn(goodEtcdClientURL)
		if err != nil {
			return nil, err
		}

		etcdMembers, err = etcdClient.ListMembers()
		if err != nil {
			return nil, err
		}

		kvs := make([]string, 0)
		for _, etcdMember := range etcdMembers {
			// ignore unstarted peers
			if len(etcdMember.Name) == 0 {
				continue
			}
			kvs = append(kvs, fmt.Sprintf("%s=%s", etcdMember.Name, etcdMember.PeerURLs[0]))
		}
		instancePeerURL, err := getPeerURL(me.Address, tmplPeerUrl)
		if err != nil {
			return nil, err
		}
		kvs = append(kvs, fmt.Sprintf("%s=%s", me.Name, instancePeerURL))

		initialClusterState = "existing"
		initialCluster = strings.Join(kvs, ",")

		//
		// join an existing cluster
		//

		if isAddRequired {
			log.Printf("adding etcd member: %s...", instancePeerURL)

			etcdClient, err := newClientFn(goodEtcdClientURL)
			if err != nil {
				return nil, err
			}

			member, err := etcdClient.AddMember(instancePeerURL)
			if member == nil {
				return nil, err
			}
			log.Printf("done\n")
		}
	} else {
		log.Printf("creating new cluster\n")

		// initial cluster
		kvs := make([]string, 0)
		for _, member := range members {
			peerURL, err := getPeerURL(member.Address, tmplPeerUrl)
			if err != nil {
				return nil, err
			}
			kvs = append(kvs, fmt.Sprintf("%s=%s", member.Name, peerURL))
		}

		initialClusterState = "new"
		initialCluster = strings.Join(kvs, ",")
	}

	kvs := map[string]string{}
	kvs["ETCD_NAME"] = me.Name
	kvs["ETCD_INITIAL_CLUSTER_STATE"] = initialClusterState
	kvs["ETCD_INITIAL_CLUSTER"] = initialCluster

	return kvs, nil
}

func writeEnvironmentFile(kvs map[string]string, w io.Writer) error {
	var buffer bytes.Buffer

	// indicate it's going to write envvars
	log.Printf("writing environment variables...")

	for k, v := range kvs {
		buffer.WriteString(fmt.Sprintf("%s=%s\n", k, v))
	}

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

func getPeerURL(hostname string, tmpl *template.Template) (string, error) {
	if tmpl != nil {
		return executeTemplate(tmpl, hostname)
	} else {
		return util.EtcdPeerURLFromIP(hostname), nil
	}
}

func getListenURL(hostname string, tmpl *template.Template) (string, error) {
	if tmpl != nil {
		return executeTemplate(tmpl, hostname)
	} else {
		return util.EtcdClientURLFromIP(hostname), nil
	}
}

func executeTemplate(tmpl *template.Template, data interface{}) (string, error) {
	var cmdBuffer bytes.Buffer
	if err := tmpl.Execute(&cmdBuffer, data); err != nil {
		return "", err
	}
	return cmdBuffer.String(), nil
}