package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"strings"

	dhcp "github.com/insomniacslk/dhcp/dhcpv4/nclient4"
	"github.com/sirupsen/logrus"
	"github.com/vishvananda/netlink"
)

const l0 = "l0"

func setupNetwork() error {
	links, err := netlink.LinkList()
	if err != nil {
		return fmt.Errorf("error getting link list: %w", err)
	}
	for _, link := range links {
		attrs := link.Attrs()
		if attrs.Name == l0 {
			continue
		}

		logger := logrus.WithField("link", attrs.Name)
		logger.Infof("Preparing link")
		if err := netlink.LinkSetUp(link); err != nil {
			return fmt.Errorf("error setting link %q to up state: %w", attrs.Name, err)
		}

		logger.Debug("Creating DHCP client")
		client, err := dhcp.New(attrs.Name)
		if err != nil {
			return fmt.Errorf("error creating DHCP client: %w", err)
		}

		logger.Debug("Requesting DHCP lease")
		lease, err := client.Request(context.TODO())
		if err != nil {
			return fmt.Errorf("error requesting DHCP: %w", err)
		}

		logger.WithField("addr", lease.ACK.YourIPAddr).Debug("Adding address to link")
		if err := netlink.AddrAdd(link, &netlink.Addr{
			IPNet: &net.IPNet{
				IP:   lease.ACK.YourIPAddr,
				Mask: lease.ACK.SubnetMask(),
			},
			Label:     attrs.Name,
			Flags:     int(lease.ACK.Flags),
			Broadcast: lease.ACK.BroadcastAddress(),
		}); err != nil {
			return fmt.Errorf("error adding address to link: %w", err)
		}

		if len(lease.ACK.DNS()) > 0 {
			logger.Info("Setting DNS servers from DHCP lease")
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
		break
	}

	lo, err := netlink.LinkByName("lo")
	if err != nil {
		return fmt.Errorf("error getting link %q: %w", "lo", err)
	}
	if err := netlink.LinkSetUp(lo); err != nil {
		return fmt.Errorf("error setting link %q to up state: %w", "lo", err)
	}

	return nil
}
