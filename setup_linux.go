// +build linux
package main

import (
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"

	"./pinlib"
	"github.com/vishvananda/netlink"
)

// This file mainly contains helper functions for client and server side setup after the
// handshake connection is established

func getDefaultRoutes(addr string) ([]netlink.Route, error) {
	ipaddr, err := net.ResolveTCPAddr("tcp", addr)
	if err != nil {
		return nil, err
	}
	return netlink.RouteGet(ipaddr.IP)
}

func getDefaultGateway(addr string) (net.IP, error) {
	routes, err := getDefaultRoutes(addr)
	if err != nil {
		return nil, err
	}
	if len(routes) == 0 {
		return nil, errors.New("no route to host")
	}
	return routes[0].Gw, nil
}

func getDefaultLinkDevIndex() (int, error) {
	routes, err := getDefaultRoutes("8.8.8.8:53")
	if err != nil {
		return -1, err
	}
	if len(routes) == 0 {
		return -1, errors.New("no route to host")
	}

	return routes[0].LinkIndex, nil
}

func SkipRemoteRouting(addr string) error {
	gw, err := getDefaultGateway(addr)
	if err != nil {
		return err
	}

	ta, err := net.ResolveTCPAddr("tcp", addr)
	if err != nil {
		return err
	}

	err = netlink.RouteAdd(&netlink.Route{
		Dst: &net.IPNet{
			IP:   ta.IP,
			Mask: net.IPv4Mask(255, 255, 255, 255),
		},
		Gw: gw,
	})

	if err != nil {
		if !os.IsExist(err) {
			return err
		}
	}

	return nil
}

func SetupRoutes(remotegw string) error {
	gw, err := net.ResolveIPAddr("ip4", remotegw)
	if err != nil {
		return err
	}
	err = netlink.RouteAdd(&netlink.Route{
		Dst: &net.IPNet{
			IP:   []byte{0, 0, 0, 0},
			Mask: net.IPv4Mask(128, 0, 0, 0),
		},
		Gw: gw.IP,
	})

	if err != nil {
		return err
	}

	return netlink.RouteAdd(&netlink.Route{
		Dst: &net.IPNet{
			IP:   []byte{128, 0, 0, 0},
			Mask: net.IPv4Mask(128, 0, 0, 0),
		},
		Gw: gw.IP,
	})
}

func SetupAddr(ifaceName string, ifaceAddr string, remotegw string) error {
	// get the link holder
	link, err := netlink.LinkByName(ifaceName)
	if err != nil {
		return err
	}

	addr, err := netlink.ParseAddr(ifaceAddr)
	if err != nil {
		return err
	}

	if remotegw != "" {

		ipaddr, err := net.ResolveIPAddr("ip4", remotegw)
		if err != nil {
			return err
		}
		addr.Peer = &net.IPNet{IP: ipaddr.IP, Mask: net.IPv4Mask(255, 255, 255, 255)}

	}
	return netlink.AddrAdd(link, addr)
}

func SetupLink(ifaceName string) error {
	// get the link holder
	link, err := netlink.LinkByName(ifaceName)
	if err != nil {
		return err
	}

	// set the mtu
	err = netlink.LinkSetMTU(link, 1500)
	if err != nil {
		return err
	}

	// activate it
	return netlink.LinkSetUp(link)

}

func SetupIPTables(ifaceName string) error {
	// iptables -F
	cmd, err := findExecutablePath("iptables")
	if err != nil {
		return fmt.Errorf("probably iptables command is missing from your system (?) or not found in the $PATH, make sure it is available : %s", err)
	}

	ix, err := getDefaultLinkDevIndex()
	if err != nil {
		return err
	}

	link, err := netlink.LinkByIndex(ix)
	if err != nil {
		return err
	}

	cmds := [][]string{
		{"-F"},                                                                          // Flush any old rules
		{"-F", "-t", "nat"},                                                             // Flush the same for the NAT table
		{"-I", "FORWARD", "-i", ifaceName, "-j", "ACCEPT"},                              // Accept all input packets from "interface" in the FORWARD chain
		{"-I", "FORWARD", "-o", ifaceName, "-j", "ACCEPT"},                              // Accept all output packets from "interface" in the FORWARD chain
		{"-I", "INPUT", "-i", ifaceName, "-j", "ACCEPT"},                                // Accept all output packets from "interface" in the INPUT chain
		{"-t", "nat", "-I", "POSTROUTING", "-o", link.Attrs().Name, "-j", "MASQUERADE"}, // It says what it does ;)
	}

	for _, cx := range cmds {
		fmt.Println("running command : ", strings.Join(append([]string{cmd}, cx...), " "))
		err := exec.Command(cmd, cx...).Start()
		if err != nil {
			return fmt.Errorf("Error while running iptables : %s", err)
		}
	}

	return nil
}

func SetupClient(client *pinlib.Client, addr, ifaceName, tunaddr, gw string) {
	client.Hook = func() error {
		err := SkipRemoteRouting(addr)
		if err != nil {
			return err
		}

		err = SetupLink(ifaceName)
		if err != nil {
			return err
		}

		err = SetupAddr(ifaceName, tunaddr, gw)
		if err != nil {
			return err
		}

		return SetupRoutes(gw)
	}
}

func SetupServer(server *pinlib.Server, ifaceName, tunaddr string) error {
	err := SetupLink(ifaceName)
	if err != nil {
		return err
	}

	err = SetupAddr(ifaceName, tunaddr, "")
	if err != nil {
		return err
	}

	return SetupIPTables(ifaceName)
}
