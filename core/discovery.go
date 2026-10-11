package core

import (
	"encoding/json"
	"net"
	"strconv"
	"time"
)

const discoveryMagic = "lansync/1"

type announcement struct {
	Magic string `json:"magic"`
	ID    string `json:"id"`
	Name  string `json:"name"`
	Port  int    `json:"port"`
}

// startDiscovery broadcasts an announcement every 30s on the LAN and listens for others.
func (n *Node) startDiscovery() {
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{Port: n.opts.DiscoveryPort})
	if err != nil {
		n.logf("discovery disabled: %v", err)
		return
	}
	n.udp = conn
	go n.readAnnouncements(conn)
	go func() {
		t := time.NewTicker(30 * time.Second)
		defer t.Stop()
		for {
			n.Announce()
			select {
			case <-n.ctx.Done():
				return
			case <-t.C:
			}
		}
	}()
}

func (n *Node) announcement() []byte {
	n.mu.Lock()
	a := announcement{Magic: discoveryMagic, ID: n.cfg.DeviceID, Name: n.cfg.DeviceName, Port: n.opts.PeerPort}
	n.mu.Unlock()
	b, _ := json.Marshal(a)
	return b
}

// Announce broadcasts this device on all local networks now.
func (n *Node) Announce() {
	if n.udp == nil {
		return
	}
	msg := n.announcement()
	for _, ip := range broadcastAddrs() {
		n.udp.WriteToUDP(msg, &net.UDPAddr{IP: ip, Port: n.opts.DiscoveryPort})
	}
}

func (n *Node) readAnnouncements(conn *net.UDPConn) {
	buf := make([]byte, 2048)
	for {
		nr, src, err := conn.ReadFromUDP(buf)
		if err != nil {
			return // closed
		}
		var a announcement
		if json.Unmarshal(buf[:nr], &a) != nil || a.Magic != discoveryMagic || a.Port <= 0 {
			continue
		}
		n.mu.Lock()
		prev, known := n.seen[a.ID]
		self := a.ID == n.cfg.DeviceID
		n.mu.Unlock()
		if self {
			continue
		}
		n.markSeen(a.ID, a.Name, net.JoinHostPort(src.IP.String(), strconv.Itoa(a.Port)))
		// Answer newcomers directly so both sides see each other without waiting 30s.
		if !known || time.Since(prev.LastSeen) > time.Minute {
			conn.WriteToUDP(n.announcement(), &net.UDPAddr{IP: src.IP, Port: n.opts.DiscoveryPort})
		}
	}
}

// broadcastAddrs returns the limited broadcast address plus each interface's directed broadcast.
// net.Interfaces can fail on newer Android versions; then only 255.255.255.255 is used.
func broadcastAddrs() []net.IP {
	out := []net.IP{net.IPv4bcast}
	ifaces, err := net.Interfaces()
	if err != nil {
		return out
	}
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagBroadcast == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := ifc.Addrs()
		for _, a := range addrs {
			ipn, ok := a.(*net.IPNet)
			if !ok || ipn.IP.To4() == nil {
				continue
			}
			ip, mask := ipn.IP.To4(), ipn.Mask
			if len(mask) == net.IPv6len {
				mask = mask[12:]
			}
			b := make(net.IP, 4)
			for i := range b {
				b[i] = ip[i] | ^mask[i]
			}
			out = append(out, b)
		}
	}
	return out
}
