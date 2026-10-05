package store

import (
	"encoding/json"
	"fmt"
	"net"
	"runtime"
	"testing"
)

func assertMeteringPortReserved(t *testing.T, port int, reserved bool) {
	t.Helper()
	address := fmt.Sprintf("127.0.0.1:%d", port)
	tcp, tcpErr := net.Listen("tcp4", address)
	if tcp != nil {
		tcp.Close()
	}
	udp, udpErr := net.ListenPacket("udp4", address)
	if udp != nil {
		udp.Close()
	}
	if reserved && (tcpErr == nil || udpErr == nil) {
		t.Fatalf("port %d was not reserved on both transports: TCP=%v UDP=%v", port, tcpErr, udpErr)
	}
	if !reserved && (tcpErr != nil || udpErr != nil) {
		t.Fatalf("released port %d could not bind: TCP=%v UDP=%v", port, tcpErr, udpErr)
	}
}

func TestMeteringFixturePortsReserveTCPAndUDPOutsideEphemeralRange(t *testing.T) {
	inbound, stats, future := meteringTestPort(t), meteringTestPort(t), meteringTestPort(t)
	if inbound == stats || inbound == future || stats == future {
		t.Fatal("a planned listener port was issued twice")
	}
	low, high := meteringEphemeralPortRange(t)
	for _, port := range []int{inbound, stats, future} {
		assertMeteringPortReserved(t, port, true)
		if port >= low && port <= high {
			t.Fatalf("test listener %d lies in the ephemeral range %d..%d", port, low, high)
		}
	}
	if runtime.GOOS == "linux" {
		t.Logf("listener ports excluded the actual Linux ephemeral source range %d..%d", low, high)
	}
	api := fmt.Sprintf("127.0.0.1:%d", stats)
	raw, err := json.Marshal(map[string]any{
		"inbounds":     []any{map[string]any{"listen_port": inbound}},
		"experimental": map[string]any{"v2ray_api": map[string]any{"listen": api}},
	})
	if err != nil {
		t.Fatal(err)
	}
	releaseMeteringConfigPorts(t, raw, api)
	assertMeteringPortReserved(t, inbound, false)
	assertMeteringPortReserved(t, stats, false)
	// Starting this core must not release a later core's planned port.
	assertMeteringPortReserved(t, future, true)
	next := meteringTestPort(t)
	if next == inbound || next == stats || next == future {
		t.Fatal("allocator reissued a listener port used by this test process")
	}
	assertMeteringPortReserved(t, next, true)
}

func TestMeteringFixtureUnusedPortReservationIsCleanedUp(t *testing.T) {
	var port int
	t.Run("reserve-without-starting-core", func(t *testing.T) {
		port = meteringTestPort(t)
		assertMeteringPortReserved(t, port, true)
	})
	assertMeteringPortReserved(t, port, false)
}
