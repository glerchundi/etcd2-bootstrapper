package pkg

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"net/url"
	"strings"
	"text/template"
	"os"

	log "github.com/glerchundi/logrus"
	"github.com/coreos/etcd/client"
	"github.com/glerchundi/etcd2-bootstrapper/pkg/etcd"
	"github.com/glerchundi/etcd2-bootstrapper/pkg/util"
)

type Config struct {
	Me string
	Members string
	TmplListenUrl string
	TmplPeerUrl string
	CertFile string
	KeyFile string
	CAFile string
	Force bool
	Out string
}

func NewConfig() *Config {
	return &Config{
		Me: "",
		Members: "",
		TmplListenUrl: "",
		TmplPeerUrl: "",
		CertFile: "",
		KeyFile: "",
		CAFile: "",
		Force: false,
		Out: "/etc/sysconfig/etcd-peers",
	}
}

type Etcd2Bootstrapper struct {
	// Configuration
	config *Config
}

func NewEtcd2Bootstrapper(config *Config) *Etcd2Bootstrapper {
	return &Etcd2Bootstrapper{config:config}
}

func (eb *Etcd2Bootstrapper) Run() {
	// sanity checks
	if eb.config.Me == "" {
		log.Fatal(fmt.Errorf("-me is a required parameters"))
	}

	meMember, err := parseMember(eb.config.Me)
	if err != nil {
		log.Fatal(err)
	}

	if eb.config.Members == "" {
		log.Fatal(fmt.Errorf("-members is a required parameters"))
	}

	clusterMembers, err := parseMembers(eb.config.Members)
	if err != nil {
		log.Fatal(err)
	}

	var tmplListen *template.Template = nil
	if eb.config.TmplListenUrl != "" {
		tmplListen, _ = template.New("tmpl-listen-url").Parse(eb.config.TmplListenUrl)
	}

	var tmplPeer *template.Template = nil
	if eb.config.TmplPeerUrl != "" {
		tmplPeer, _ = template.New("tmpl-listen-url").Parse(eb.config.TmplPeerUrl)
	}

	if _, err := os.Stat(eb.config.Out); err == nil {
		log.Printf("etcd-peers file %s already created, exiting.\n", eb.config.Out)
		return
	}

	// create client instantiator

	var newClientFn func(string)(*etcd.Client,error) = nil
	if eb.config.CertFile != "" && eb.config.KeyFile != "" && eb.config.CAFile != "" {
		newClientFn = func(url string)(*etcd.Client,error) {
			return etcd.NewTLSClient(url, eb.config.CertFile, eb.config.KeyFile, eb.config.CAFile)
		}
	} else {
		newClientFn = func(url string)(*etcd.Client,error) {
			return etcd.NewClient(url)
		}
	}

	kvs, err := syncEtcdCluster(*meMember, clusterMembers, eb.config.Force, tmplListen, tmplPeer, newClientFn)
	if err != nil {
		log.Fatal(err)
	}

	tempFilePath := eb.config.Out + ".tmp"
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

	if err := os.Rename(tempFilePath, eb.config.Out); err != nil {
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
