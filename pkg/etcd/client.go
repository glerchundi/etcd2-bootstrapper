// Copyright 2015 CoreOS, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//
// Modified by: Gorka Lerchundi Osa

package etcd

import (
	"net/http"
	"time"

	"github.com/coreos/etcd/client"
	"github.com/coreos/etcd/pkg/transport"
	"golang.org/x/net/context"
)

const (
	// the maximum amount of time a dial will wait for a connection to setup.
	// 30s is long enough for most of the network conditions.
	defaultDialTimeout = 30 * time.Second
)

type Client struct
{
	c client.Client
	membersAPI client.MembersAPI
}

func NewClient(url string) (*Client, error) {
	tr, err := transport.NewTransport(transport.TLSInfo{}, defaultDialTimeout)
	if err != nil {
		return nil, err
	}
	return newClient(url, tr)
}

func NewTLSClient(url string, certFile, keyFile, caCertFile string) (*Client, error) {
	tr, err := transport.NewTransport(
		transport.TLSInfo{
			CAFile:   caCertFile,
			CertFile: certFile,
			KeyFile:  keyFile,
		},
		defaultDialTimeout,
	)
	if err != nil {
		return nil, err
	}
	return newClient(url, tr)
}

func newClient(url string, transport *http.Transport) (*Client, error) {
	cfg := client.Config{
		Transport: transport,
		Endpoints: []string{url},
	}

	c, err := client.New(cfg)
	if err != nil {
		return nil, err
	}

	return &Client{c,client.NewMembersAPI(c)}, nil
}

func contextWithTotalTimeout() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), client.DefaultRequestTimeout)
}

func (c *Client) ListMembers() (members []client.Member, err error) {
	ctx, cancel := contextWithTotalTimeout()
	defer cancel()
	members, err = c.membersAPI.List(ctx)
	cancel()
	return
}

func (c *Client) AddMember(peerURL string) (member *client.Member, err error) {
	ctx, cancel := contextWithTotalTimeout()
	member, err = c.membersAPI.Add(ctx, peerURL)
	cancel()
	return
}

func (c *Client) RemoveMember(removalID string) (err error) {
	ctx, cancel := contextWithTotalTimeout()
	err = c.membersAPI.Remove(ctx, removalID)
	cancel()
	return
}