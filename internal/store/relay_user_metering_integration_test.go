package store

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"

	"qingzhou/internal/sbstats"
	"qingzhou/internal/singbox"
)

// Exercise RFC 1928 UDP ASSOCIATE through the existing mixed listener. No TUN,
// system routes, production endpoints or third-party proxy clients are used.
func relayFixtureUDPEcho(proxy, username, password string, target *net.UDPAddr, payload []byte) error {
	control, err := net.DialTimeout("tcp", proxy, 5*time.Second)
	if err != nil {
		return err
	}
	defer control.Close()
	if err = control.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		return err
	}
	method := byte(0)
	if username != "" {
		method = 2
	}
	if _, err = control.Write([]byte{5, 1, method}); err != nil {
		return err
	}
	var response [2]byte
	if _, err = io.ReadFull(control, response[:]); err != nil {
		return err
	}
	if response != [2]byte{5, method} {
		return fmt.Errorf("SOCKS method rejected: %v", response)
	}
	if method == 2 {
		auth := append([]byte{1, byte(len(username))}, []byte(username)...)
		auth = append(auth, byte(len(password)))
		auth = append(auth, []byte(password)...)
		if _, err = control.Write(auth); err != nil {
			return err
		}
		if _, err = io.ReadFull(control, response[:]); err != nil {
			return err
		}
		if response != [2]byte{1, 0} {
			return fmt.Errorf("SOCKS fixture authentication rejected")
		}
	}
	if _, err = control.Write([]byte{5, 3, 0, 1, 0, 0, 0, 0, 0, 0}); err != nil {
		return err
	}
	var header [4]byte
	if _, err = io.ReadFull(control, header[:]); err != nil {
		return err
	}
	if header[0] != 5 || header[1] != 0 || header[2] != 0 {
		return fmt.Errorf("UDP association rejected: %v", header)
	}
	readAddress := func(reader io.Reader, atyp byte) (*net.UDPAddr, error) {
		var size int
		switch atyp {
		case 1:
			size = 4
		case 4:
			size = 16
		default:
			return nil, fmt.Errorf("fixture expected numeric SOCKS address, got type %d", atyp)
		}
		address := make([]byte, size+2)
		if _, err := io.ReadFull(reader, address); err != nil {
			return nil, err
		}
		return &net.UDPAddr{IP: net.IP(address[:size]), Port: int(binary.BigEndian.Uint16(address[size:]))}, nil
	}
	bound, err := readAddress(control, header[3])
	if err != nil {
		return err
	}
	if bound.IP.IsUnspecified() {
		bound.IP = net.IPv4(127, 0, 0, 1)
	}
	udp, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		return err
	}
	defer udp.Close()
	if err = udp.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		return err
	}
	packet := []byte{0, 0, 0, 1}
	packet = append(packet, target.IP.To4()...)
	packet = binary.BigEndian.AppendUint16(packet, uint16(target.Port))
	packet = append(packet, payload...)
	for i := 0; i < 3; i++ {
		if _, err = udp.WriteToUDP(packet, bound); err != nil {
			return err
		}
		buffer := make([]byte, 4096)
		n, _, err := udp.ReadFromUDP(buffer)
		if err != nil {
			return err
		}
		if n < 4 || !bytes.Equal(buffer[:3], []byte{0, 0, 0}) {
			return fmt.Errorf("malformed UDP reply")
		}
		reader := bytes.NewReader(buffer[4:n])
		from, err := readAddress(reader, buffer[3])
		if err != nil {
			return err
		}
		body, err := io.ReadAll(reader)
		if err != nil || !from.IP.Equal(target.IP) || from.Port != target.Port || !bytes.Equal(body, payload) {
			return fmt.Errorf("UDP echo payload or destination mismatch: %v", err)
		}
	}
	return nil
}

// The mixed clients below are local test drivers, not billed machines. Both
// original VLESS UUIDs enter the SAME physical inbound and traverse the SAME
// relay links; only their downstream wire identities distinguish the owners.
func TestMeteringRelayRealSingboxSharedUserPath(t *testing.T) {
	bin := os.Getenv("QZ_SINGBOX_TEST_BIN")
	if bin == "" {
		t.Skip("set QZ_SINGBOX_TEST_BIN for loopback integration")
	}
	for _, scenario := range []struct {
		entryType string
		machines  int
	}{{"mixed", 2}, {"vless", 2}, {"vless", 3}} {
		t.Run(fmt.Sprintf("%s/%d-machines", scenario.entryType, scenario.machines), func(t *testing.T) {
			st := newRefundStore(t)
			st.SetSecretKey([]byte("per-user-relay-fixture"))
			if err := st.SetSetting(SettingBlockPrivateEgress, "0"); err != nil {
				t.Fatal(err)
			}
			type machine struct {
				id      int64
				inbound int64
				port    int
				tag     string
				api     string
				stats   *sbstats.Client
			}
			machines := make([]machine, scenario.machines)
			for i := range machines {
				id, err := st.CreateServer(Server{Name: fmt.Sprintf("fixture-hop-%d", i), Host: "127.0.0.1", Enabled: true})
				if err != nil {
					t.Fatal(err)
				}
				machines[i] = machine{id: id, port: meteringTestPort(t), tag: fmt.Sprintf("user-relay-hop-%d", i), api: fmt.Sprintf("127.0.0.1:%d", meteringTestPort(t))}
			}
			for i := len(machines) - 1; i >= 0; i-- {
				m := &machines[i]
				kind := "vless"
				if i == 0 {
					kind = scenario.entryType
				}
				var upstream int64
				if i+1 < len(machines) {
					upstream = machines[i+1].inbound
				}
				var err error
				m.inbound, err = st.SaveSbInbound(&SbInbound{ServerID: m.id, Type: kind, Tag: m.tag, Listen: "127.0.0.1", ListenPort: m.port, Options: `{}`, Enabled: true, UpstreamInboundID: upstream})
				if err != nil {
					t.Fatal(err)
				}
			}
			pkg := mkPlan(t, st, "shared-path", 10, 10, 30)
			bindPlanToInbound(t, st, pkg.ID, machines[0].tag)
			type customer struct {
				uid           int64
				bucketID      int64
				original      *User
				wire          singbox.User
				client        *http.Client
				payload       int
				entryHop      int
				socksAddress  string
				socksUsername string
				socksPassword string
			}
			customerCount := 2
			if scenario.machines == 3 {
				customerCount = 3
			}
			customers := make([]customer, customerCount)
			for i := range customers {
				entryHop := 0
				customerPlan := pkg
				if i == 2 {
					entryHop = 1
					customerPlan = mkPlan(t, st, "direct-middle", 10, 10, 30)
					bindPlanToInbound(t, st, customerPlan.ID, machines[1].tag)
				}
				uid, bid := trafficCompatCustomer(t, st, customerPlan, "account", fmt.Sprintf("fixture_customer_%d", i))
				if _, err := st.SetSubTokenIfEmpty(uid, fmt.Sprintf("fixture-subscription-%d", i)); err != nil {
					t.Fatal(err)
				}
				u, err := st.UserByID(uid)
				if err != nil {
					t.Fatal(err)
				}
				customers[i] = customer{uid: uid, bucketID: bid, original: u, payload: (i + 1) * 65536, entryHop: entryHop}
			}
			users, err := st.BuildUsersByTag(time.Now().Unix())
			if err != nil {
				t.Fatal(err)
			}
			for i := range customers {
				c := &customers[i]
				vlessEntry := scenario.entryType == "vless" || c.entryHop > 0
				for _, u := range users[machines[c.entryHop].tag] {
					if (vlessEntry && u.UUID == c.original.ClientUUID.String) || (!vlessEntry && u.Name == c.original.ProxyUsername) {
						c.wire = u
						break
					}
				}
				if c.wire.Name == "" {
					t.Fatalf("original customer %d absent from shared entry", c.uid)
				}
			}
			if err = st.ConfigureTrafficMetering(true, true, true); err != nil {
				t.Fatal(err)
			}
			if err = st.PrepareRelayMetering(); err != nil {
				t.Fatal(err)
			}
			for i := len(machines) - 1; i >= 0; i-- {
				m := &machines[i]
				raw, err := st.BuildSingboxConfigForServer(m.id, singbox.DefaultBaseConfig, m.api, users)
				if err != nil {
					t.Fatal(err)
				}
				startMeteringBox(t, bin, raw, m.api)
				if err = st.RecordRelayConfigApplied(m.id, raw); err != nil {
					t.Fatal(err)
				}
				m.stats = sbstats.New(m.api)
				t.Cleanup(func() { m.stats.Close() })
			}
			// A single physical link per hop must carry both users independently.
			links, err := st.RelayMeteringLinks()
			if err != nil || len(links) != len(machines)-1 {
				t.Fatalf("expected shared path with %d links: %+v %v", len(machines)-1, links, err)
			}
			counterNames := make([]map[int64]string, len(machines))
			for i := range machines {
				counterNames[i] = map[int64]string{}
				for _, c := range customers {
					if c.entryHop == i {
						counterNames[i][c.uid] = c.wire.Name
					}
				}
				if i == 0 {
					continue
				}
				rows, err := st.db.Query(`SELECT u.user_id,u.identity_name FROM relay_metering_users u JOIN relay_metering_links l ON l.id=u.link_id WHERE l.target_server_id=? AND u.enabled=1`, machines[i].id)
				if err != nil {
					t.Fatal(err)
				}
				for rows.Next() {
					var uid int64
					var name string
					if err = rows.Scan(&uid, &name); err != nil {
						rows.Close()
						t.Fatal(err)
					}
					if _, exists := counterNames[i][uid]; exists {
						t.Fatalf("owner %d has duplicate wire mappings on hop %d", uid, i)
					}
					counterNames[i][uid] = name
				}
				if err = rows.Close(); err != nil {
					t.Fatal(err)
				}
				if err = rows.Err(); err != nil || len(counterNames[i]) != len(customers) || counterNames[i][customers[0].uid] == counterNames[i][customers[1].uid] {
					t.Fatalf("hop %d did not retain distinct owners: %v %v", i, counterNames[i], err)
				}
			}
			for i := range customers {
				c := &customers[i]
				proxyPort := machines[c.entryHop].port
				proxy := &url.URL{Scheme: "http", Host: fmt.Sprintf("127.0.0.1:%d", proxyPort), User: url.UserPassword(c.wire.Name, c.wire.Password)}
				c.socksUsername, c.socksPassword = c.wire.Name, c.wire.Password
				if scenario.entryType == "vless" || c.entryHop > 0 {
					proxyPort = meteringTestPort(t)
					api := fmt.Sprintf("127.0.0.1:%d", meteringTestPort(t))
					clientConfig := map[string]any{
						"log":          map[string]any{"level": "warn"},
						"inbounds":     []any{map[string]any{"type": "mixed", "tag": "fixture-driver", "listen": "127.0.0.1", "listen_port": proxyPort}},
						"outbounds":    []any{map[string]any{"type": "vless", "tag": "original-customer", "server": "127.0.0.1", "server_port": machines[c.entryHop].port, "uuid": c.original.ClientUUID.String}},
						"route":        map[string]any{"final": "original-customer"},
						"experimental": map[string]any{"v2ray_api": map[string]any{"listen": api, "stats": map[string]any{"enabled": true, "outbounds": []string{"original-customer"}}}},
					}
					raw, err := json.Marshal(clientConfig)
					if err != nil {
						t.Fatal(err)
					}
					startMeteringBox(t, bin, raw, api)
					proxy = &url.URL{Scheme: "http", Host: fmt.Sprintf("127.0.0.1:%d", proxyPort)}
					c.socksUsername, c.socksPassword = "", ""
				}
				c.socksAddress = fmt.Sprintf("127.0.0.1:%d", proxyPort)
				transport := &http.Transport{Proxy: http.ProxyURL(proxy), DisableKeepAlives: true}
				t.Cleanup(transport.CloseIdleConnections)
				c.client = &http.Client{Transport: transport, Timeout: 5 * time.Second}
			}
			destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if _, err := io.Copy(io.Discard, r.Body); err != nil {
					http.Error(w, "fixture upload failed", http.StatusBadRequest)
					return
				}
				n, _ := strconv.Atoi(r.URL.Query().Get("bytes"))
				if n != 65536 && n != 131072 && n != 196608 {
					http.Error(w, "unexpected fixture size", http.StatusBadRequest)
					return
				}
				w.Header().Set("Content-Length", strconv.Itoa(n))
				_, _ = w.Write(bytes.Repeat([]byte{'x'}, n))
			}))
			defer destination.Close()
			request := func(c customer) error {
				response, err := c.client.Post(fmt.Sprintf("%s/?bytes=%d", destination.URL, c.payload), "application/octet-stream", bytes.NewReader(bytes.Repeat([]byte{'u'}, c.payload/256)))
				if err != nil {
					return err
				}
				defer response.Body.Close()
				body, err := io.ReadAll(response.Body)
				if err != nil || response.StatusCode != http.StatusOK || len(body) != c.payload || !bytes.Equal(body, bytes.Repeat([]byte{'x'}, c.payload)) {
					return fmt.Errorf("owner %d response status=%s bytes=%d error=%v", c.uid, response.Status, len(body), err)
				}
				return nil
			}
			type snapshot []map[string]*sbstats.Traffic
			read := func() snapshot {
				t.Helper()
				out := make(snapshot, len(machines))
				for i, m := range machines {
					ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					var err error
					out[i], err = m.stats.QueryTraffic(ctx, false)
					cancel()
					if err != nil {
						t.Fatal(err)
					}
				}
				return out
			}
			get := func(s snapshot, hop int, uid int64) UsageDelta {
				v := s[hop][counterNames[hop][uid]]
				if v == nil {
					return UsageDelta{}
				}
				return UsageDelta{Up: v.Up, Down: v.Down}
			}
			if err := request(customers[0]); err != nil {
				t.Fatal(err)
			}
			first := read()
			for hop := range machines {
				a, b := get(first, hop, customers[0].uid), get(first, hop, customers[1].uid)
				if a.Down < int64(customers[0].payload) || a.Up == 0 || b != (UsageDelta{}) {
					t.Fatalf("first user crossed attribution on hop %d: A=%+v B=%+v", hop, a, b)
				}
			}
			if err := request(customers[1]); err != nil {
				t.Fatal(err)
			}
			second := read()
			for hop := range machines {
				a, b := get(second, hop, customers[0].uid), get(second, hop, customers[1].uid)
				if a != get(first, hop, customers[0].uid) || b.Down < int64(customers[1].payload) || b.Up == 0 || b.Down <= a.Down {
					t.Fatalf("second user altered another owner's counter on hop %d: A=%+v B=%+v", hop, a, b)
				}
			}
			if len(customers) == 3 {
				if err := request(customers[2]); err != nil {
					t.Fatal(err)
				}
				third := read()
				if get(third, 0, customers[2].uid) != (UsageDelta{}) {
					t.Fatal("direct-middle customer appeared on the upstream machine")
				}
				for hop := 1; hop < len(machines); hop++ {
					if get(third, hop, customers[2].uid).Down < int64(customers[2].payload) {
						t.Fatalf("direct-middle customer missing on hop %d", hop)
					}
					for _, c := range customers[:2] {
						if get(third, hop, c.uid) != get(second, hop, c.uid) {
							t.Fatal("direct-middle customer changed a relayed customer's counter")
						}
					}
				}
			}
			// Repeated simultaneous independent connections exercise auth routing,
			// rather than merely showing that one warm connection can pass bytes.
			var wg sync.WaitGroup
			errors := make(chan error, 4*len(customers))
			for round := 0; round < 4; round++ {
				for _, c := range customers {
					wg.Add(1)
					go func(c customer) { defer wg.Done(); errors <- request(c) }(c)
				}
			}
			wg.Wait()
			close(errors)
			for err := range errors {
				if err != nil {
					t.Fatal(err)
				}
			}
			finalTCP := read()
			echo, err := net.ListenPacket("udp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { echo.Close() })
			go func() {
				buf := make([]byte, 4096)
				for {
					n, from, err := echo.ReadFrom(buf)
					if err != nil {
						return
					}
					if _, err = echo.WriteTo(buf[:n], from); err != nil {
						return
					}
				}
			}()
			beforeUDP := finalTCP
			for i, c := range customers {
				payload := bytes.Repeat([]byte{byte('a' + i)}, 257+i*252)
				if err = relayFixtureUDPEcho(c.socksAddress, c.socksUsername, c.socksPassword, echo.LocalAddr().(*net.UDPAddr), payload); err != nil {
					t.Fatalf("owner %d UDP: %v", c.uid, err)
				}
				afterUDP := read()
				for hop := range machines {
					for _, other := range customers {
						got, before := get(afterUDP, hop, other.uid), get(beforeUDP, hop, other.uid)
						if other.uid != c.uid || hop < c.entryHop {
							if got != before {
								t.Fatalf("UDP owner %d changed unrelated hop=%d owner=%d", c.uid, hop, other.uid)
							}
						} else if got.Up-before.Up < int64(3*len(payload)) || got.Down-before.Down < int64(3*len(payload)) {
							t.Fatalf("UDP owner %d not independently measured at hop %d: before=%+v after=%+v", c.uid, hop, before, got)
						}
					}
				}
				beforeUDP = afterUDP
			}
			final := beforeUDP
			for _, data := range []snapshot{first, second, finalTCP, final} {
				for i, m := range machines {
					deltas := map[string]UsageDelta{}
					for name, v := range data[i] {
						deltas[name] = UsageDelta{Up: v.Up, Down: v.Down}
					}
					p := NewTrafficPoll(m.id, deltas)
					p.Mode, p.Epoch = "cumulative", fmt.Sprintf("fixture-process-%d", i)
					trafficCompatRecord(t, st, p)
					trafficCompatRecord(t, st, p)
				}
			}
			for i, m := range machines {
				report, err := st.ServerServiceTraffic(m.id, 0)
				if err != nil {
					t.Fatal(err)
				}
				byUser := map[int64]UsageDelta{}
				for _, src := range report.Sources {
					got := byUser[src.UserID]
					byUser[src.UserID] = UsageDelta{Up: got.Up + src.Up, Down: got.Down + src.Down}
				}
				var total int64
				var wantBillable int64
				for _, c := range customers {
					want := get(final, i, c.uid)
					if (i >= c.entryHop && want.Down < int64(5*c.payload)) || byUser[c.uid] != want {
						t.Fatalf("hop %d owner %d report=%+v raw=%+v", i, c.uid, byUser[c.uid], want)
					}
					total += want.Up + want.Down
					if c.entryHop == i {
						wantBillable += want.Up + want.Down
					}
					t.Logf("hop=%d user=%d raw_up=%d raw_down=%d report_up=%d report_down=%d", i, c.uid, want.Up, want.Down, byUser[c.uid].Up, byUser[c.uid].Down)
				}
				if report.Total != total || report.BillableTotal != wantBillable || !report.ObservedUserCoverageComplete {
					t.Fatalf("hop %d report total=%d billable=%d observed_complete=%t; want total=%d billable=%d observed_complete=true", i, report.Total, report.BillableTotal, report.ObservedUserCoverageComplete, total, wantBillable)
				}
				t.Logf("hop=%d observed_complete=%t attribution_ready=%t coverage_reasons=%v", i, report.ObservedUserCoverageComplete, report.AttributionReady, report.CoverageReasons)
			}
			for _, c := range customers {
				want := get(final, c.entryHop, c.uid)
				trafficCompatTotals(t, st, c.uid, c.bucketID, want.Up, want.Down)
				u, err := st.UserByID(c.uid)
				if err != nil || u.ClientUUID != c.original.ClientUUID || u.ClientSecret != c.original.ClientSecret || u.ProxyUsername != c.original.ProxyUsername || u.ProxyPassword != c.original.ProxyPassword || u.SubToken != c.original.SubToken {
					t.Fatalf("owner %d original credentials/subscription changed: %v", c.uid, err)
				}
				t.Logf("user=%d quota=%d entry_raw=%d (downstream excluded)", c.uid, ledgerUsed(t, st, c.uid), want.Up+want.Down)
			}
		})
	}
}
