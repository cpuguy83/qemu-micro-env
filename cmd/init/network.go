package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"strings"

	dhcp "github.com/insomniacslk/dhcp/dhcpv4/nclient4"
	"github.com/vishvananda/netlink"
)

const ifaceName = "eth0"

func setupNetwork() error {
	link, err := netlink.LinkByName(ifaceName)
	if err != nil {
		ls, _ := net.Interfaces()
		ifaces := make([]string, 0, len(ls))
		for _, l := range ls {
			ifaces = append(ifaces, l.Name)
		}
		return fmt.Errorf("error getting link %q: %w: interfaces: %s", ifaceName, err, strings.Join(ifaces, ", "))
	}
	if err := netlink.LinkSetUp(link); err != nil {
		return fmt.Errorf("error setting link %q to up state: %w", ifaceName, err)
	}

	lo, err := netlink.LinkByName("lo")
	if err != nil {
		return fmt.Errorf("error getting link %q: %w", "lo", err)
	}
	if err := netlink.LinkSetUp(lo); err != nil {
		return fmt.Errorf("error setting link %q to up state: %w", "lo", err)
	}

	client, err := dhcp.New(ifaceName)
	if err != nil {
		return fmt.Errorf("error creating DHCP client: %w", err)
	}

	lease, err := client.Request(context.TODO())
	if err != nil {
		return fmt.Errorf("error requesting DHCP: %w", err)
	}

	if err := netlink.AddrAdd(link, &netlink.Addr{
		IPNet: &net.IPNet{
			IP:   lease.ACK.YourIPAddr,
			Mask: lease.ACK.SubnetMask(),
		},
		Label:     ifaceName,
		Flags:     int(lease.ACK.Flags),
		Broadcast: lease.ACK.BroadcastAddress(),
	}); err != nil {
		return fmt.Errorf("error adding address to link: %w", err)
	}

	if len(lease.ACK.DNS()) > 0 {
		b := strings.Builder{}
		for _, addr := range lease.ACK.DNS() {
			b.WriteString("nameserver " + addr.String() + "\n")
		}
		if err := os.WriteFile("/etc/resolv.conf", []byte(b.String()), 0644); err != nil {
			return fmt.Errorf("error writing /etc/resolv.conf: %w", err)
		}
	}
	if err := netlink.RouteAdd(&netlink.Route{
		Gw: lease.ACK.ServerIPAddr,
	}); err != nil {
		return fmt.Errorf("error adding route: %w", err)
	}

	return nil
}
