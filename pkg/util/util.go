package util

import (
	"fmt"
)

func EtcdPeerURLFromIP(ip string) string {
	return fmt.Sprintf("http://%s:2380", ip)
}

func EtcdClientURLFromIP(ip string) string {
	return fmt.Sprintf("http://%s:2379", ip)
}