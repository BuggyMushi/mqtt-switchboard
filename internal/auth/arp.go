package auth

import (
	"os"
	"strings"
)

// ARPClientEntry represents an active LAN device discovered in the router's ARP table
type ARPClientEntry struct {
	IP     string `json:"ip"`
	MAC    string `json:"mac"`
	Device string `json:"device"`
}

func readARPTable(targetIP string) string {
	data, err := os.ReadFile("/proc/net/arp")
	if err != nil {
		return ""
	}
	lines := strings.Split(string(data), "\n")
	for i, line := range lines {
		if i == 0 {
			continue // skip header
		}
		fields := strings.Fields(line)
		if len(fields) >= 4 && fields[0] == targetIP {
			mac := NormalizeMAC(fields[3])
			if mac != "00:00:00:00:00:00" && len(mac) == 17 {
				return mac
			}
		}
	}
	return ""
}

// ListARPClients returns all valid LAN ARP entries from /proc/net/arp so the admin PC can easily pick E-Ink devices
func ListARPClients() []ARPClientEntry {
	data, err := os.ReadFile("/proc/net/arp")
	if err != nil {
		return []ARPClientEntry{}
	}
	var list []ARPClientEntry
	lines := strings.Split(string(data), "\n")
	for i, line := range lines {
		if i == 0 {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) >= 6 {
			ip := fields[0]
			flags := fields[2]
			mac := NormalizeMAC(fields[3])
			dev := fields[5]
			// 0x0 means incomplete ARP entry
			if flags != "0x0" && mac != "00:00:00:00:00:00" && len(mac) == 17 {
				list = append(list, ARPClientEntry{
					IP:     ip,
					MAC:    mac,
					Device: dev,
				})
			}
		}
	}
	return list
}

