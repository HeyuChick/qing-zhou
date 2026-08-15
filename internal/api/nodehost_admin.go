package api

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// Auto-detection for 「节点对外地址」(node_host_override).
//
// nodeHost() only knows two things: the override, and the first enabled
// server's host. A node running on the panel's own machine is neither — the
// panel host is not a servers row (it needs no SSH delivery) — so the address
// resolves to empty and every subscription comes back with no self-built links
// at all. That is the case this endpoint exists for.
//
// It *suggests*, it does not decide. A silent server-side fallback would trade
// a loud failure ("共 0 个节点", obviously broken) for a quiet one (links that
// exist but never connect, e.g. a Cloudflare-proxied hostname), which is far
// more expensive to diagnose. So the candidates are handed to the admin with
// their provenance and they pick.

type nodeHostCandidate struct {
	Value       string `json:"value"`
	Source      string `json:"source"`
	Label       string `json:"label"`
	Note        string `json:"note"`
	Recommended bool   `json:"recommended"`
}

// nodeHostEchoURLs are the IP-echo services probed to learn this machine's own
// egress address. Same set the egress checker uses (see egressEchoURLs); more
// than one because any single service is blocked somewhere.
var nodeHostEchoURLs = []string{
	"https://api.ip.sb/ip",
	"https://ifconfig.me/ip",
	"https://ipinfo.io/ip",
}

// Budget: the echo probes run concurrently under one deadline, so the whole
// handler costs about this much wall-clock in the worst case. Kept far below
// the server's 30s WriteTimeout — a handler that outlives it reports failure to
// the browser while having succeeded, with nothing in the log to say so.
const nodeHostProbeTimeout = 4 * time.Second

// GET /api/admin/settings/detect-node-host — suggest values for 节点对外地址.
func (a *API) handleDetectNodeHost(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), nodeHostProbeTimeout)
	defer cancel()

	echoed := detectEgressIPs(ctx)
	out := append([]nodeHostCandidate{}, echoed...)
	out = append(out, a.configuredNodeHostCandidates()...)
	// Private NIC addresses only when the echo probes found nothing: a machine
	// with no working outbound is the LAN/intranet case, where a private address
	// really is what clients dial. Otherwise they are pure noise — a host with
	// VPN, container and hypervisor adapters has several, none of them the
	// answer, and they would bury the one that is.
	out = append(out, localInterfaceCandidates(len(echoed) == 0)...)
	out = dedupeNodeHostCandidates(out)
	// Only a measured address is endorsed. The configured 访问地址 is the one
	// candidate that is routinely wrong (橙云), so when the probes are blocked
	// and it is all that is left, it is offered — with its warning — but never
	// marked 推荐 and never filled in without the admin reading it.
	for i := range out {
		if out[i].Source == "echo" {
			out[i].Recommended = true
			break
		}
	}
	ok(w, J{"candidates": out})
}

// detectEgressIPs asks the echo services what address this machine comes from,
// once per family so a dual-stack host gets both listed separately instead of
// whichever one the dialer happened to pick.
func detectEgressIPs(ctx context.Context) []nodeHostCandidate {
	type result struct {
		family string
		ip     string
		via    string
	}
	var (
		mu   sync.Mutex
		hits []result
		wg   sync.WaitGroup
	)
	for _, family := range []string{"tcp4", "tcp6"} {
		client := echoClient(family)
		for _, u := range nodeHostEchoURLs {
			wg.Add(1)
			go func(family, u string) {
				defer wg.Done()
				ip := fetchEchoIP(ctx, client, u)
				if ip == "" {
					return
				}
				mu.Lock()
				hits = append(hits, result{family: family, ip: ip, via: echoServiceName(u)})
				mu.Unlock()
			}(family, u)
		}
	}
	wg.Wait()

	var out []nodeHostCandidate
	for _, family := range []string{"tcp4", "tcp6"} {
		for _, h := range hits {
			if h.family != family {
				continue
			}
			label := "公网出口 IPv4"
			if family == "tcp6" {
				label = "公网出口 IPv6"
			}
			out = append(out, nodeHostCandidate{
				Value:  h.ip,
				Source: "echo",
				Label:  label,
				Note:   "面板所在机器的出口地址（" + h.via + " 回显）",
			})
			break // one per family; the rest of the services are redundancy
		}
	}
	return out
}

// echoClient dials only the given family and never honors a proxy: the whole
// point is to learn *this machine's* egress address, and an HTTP_PROXY picked
// up from the environment would report the proxy's IP as if it were ours.
func echoClient(network string) *http.Client {
	dialer := &net.Dialer{Timeout: 2 * time.Second}
	return &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, addr string) (net.Conn, error) {
				host, port, err := net.SplitHostPort(addr)
				if err != nil {
					return nil, err
				}
				ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
				if err != nil {
					return nil, err
				}
				lastErr := errors.New("没有可用的 " + network + " 地址")
				for _, ip := range ips {
					v4 := ip.IP.To4() != nil
					if isInternalIP(ip.IP) || (network == "tcp4") != v4 {
						continue
					}
					c, err := dialer.DialContext(ctx, network, net.JoinHostPort(ip.IP.String(), port))
					if err == nil {
						return c, nil
					}
					lastErr = err
				}
				return nil, lastErr
			},
		},
	}
}

// fetchEchoIP returns the IP literal the service echoed back, or "" for any
// failure — a probe that cannot answer is simply one fewer candidate.
func fetchEchoIP(ctx context.Context, c *http.Client, url string) string {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return ""
	}
	resp, err := c.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ""
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 128))
	if err != nil {
		return ""
	}
	return parseEchoBody(body)
}

// parseEchoBody accepts only a public IP literal. A captive portal, an error
// page or a service that answered with something else must yield nothing —
// whatever comes back here would otherwise be written into every subscription.
func parseEchoBody(body []byte) string {
	ip := net.ParseIP(strings.TrimSpace(string(body)))
	if ip == nil || isInternalIP(ip) {
		return ""
	}
	return ip.String()
}

func echoServiceName(rawURL string) string {
	s := strings.TrimPrefix(strings.TrimPrefix(rawURL, "https://"), "http://")
	if i := strings.IndexByte(s, '/'); i > 0 {
		s = s[:i]
	}
	return s
}

// configuredNodeHostCandidates offers what the panel already knows: the
// configured 访问地址, and the server row nodeHost() falls back to today.
func (a *API) configuredNodeHostCandidates() []nodeHostCandidate {
	var out []nodeHostCandidate
	// The *configured* base only — publicBase falls back to the request's Host,
	// which is whatever the admin happens to be browsing (a LAN name, an SSH
	// tunnel's localhost:8099). That guess is fine for building a link back to
	// the panel and useless as an address clients dial.
	base := os.Getenv("QZ_PUBLIC_BASE")
	if base == "" {
		base, _ = a.st.GetSetting("public_base")
	}
	if base != "" {
		if h := hostOnly(normalizeBase(base)); h != "" && !isInternalHost(h) {
			out = append(out, nodeHostCandidate{
				Value:  h,
				Source: "public_base",
				Label:  "面板访问地址",
				// The one candidate that is routinely wrong: behind Cloudflare's
				// orange cloud this is the proxied hostname, which clients cannot
				// reach on a node port. Say so where it is read, not in a doc.
				Note: "面板挂在 Cloudflare 橙云等反代后面时不能用，要填源站 IP",
			})
		}
	}
	if servers, err := a.st.ListServers(); err == nil {
		for _, sv := range servers {
			if sv.Enabled && sv.Host != "" {
				out = append(out, nodeHostCandidate{
					Value:  sv.Host,
					Source: "server",
					Label:  "服务器「" + sv.Name + "」",
					Note:   "留空时的当前默认取值（第一台已启用服务器）",
				})
				break
			}
		}
	}
	return out
}

// isInternalHost reports whether a bare host is something no client could dial:
// a loopback/LAN IP literal, or a localhost name. Hostnames are not resolved —
// a name that only resolves internally is the admin's call to make.
func isInternalHost(h string) bool {
	if ip := net.ParseIP(h); ip != nil {
		return isInternalIP(ip)
	}
	h = strings.ToLower(h)
	return h == "localhost" || strings.HasSuffix(h, ".localhost")
}

// hostOnly strips scheme, port and path from a base URL, leaving a bare host —
// the field takes a host, not a URL.
func hostOnly(base string) string {
	s := base
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
	}
	if i := strings.IndexAny(s, "/?#"); i >= 0 {
		s = s[:i]
	}
	if h, _, err := net.SplitHostPort(s); err == nil {
		s = h
	}
	return strings.Trim(strings.TrimSpace(s), "[]")
}

// localInterfaceCandidates lists addresses configured on this machine's NICs.
// On a plain VPS the public one here matches the echo probe (and dedupes away);
// includePrivate adds the LAN addresses, which are the right answer only when
// the clients live on that LAN — see the caller.
func localInterfaceCandidates(includePrivate bool) []nodeHostCandidate {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil
	}
	var out []nodeHostCandidate
	for _, a := range addrs {
		ipn, okAddr := a.(*net.IPNet)
		if !okAddr || ipn.IP == nil {
			continue
		}
		ip := ipn.IP
		// Loopback and link-local are never reachable by a client. Note that a
		// global IPv6 here may still be a privacy/temporary address (they are
		// indistinguishable through InterfaceAddrs, which exposes no address
		// flags) — one more reason these are last resort, below a measured
		// egress address, and never the recommended pick.
		if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() {
			continue
		}
		c := nodeHostCandidate{Value: ip.String(), Source: "iface", Label: "本机网卡"}
		if isInternalIP(ip) {
			if !includePrivate {
				continue
			}
			c.Note = "内网地址，只有同一局域网的客户端能连"
		} else {
			c.Note = "网卡上直接配置的公网地址"
		}
		out = append(out, c)
		if len(out) >= 4 {
			break
		}
	}
	return out
}

// dedupeNodeHostCandidates keeps the first occurrence of each address, so a
// value that several sources agree on is shown once, attributed to the most
// trustworthy source (the slice is assembled in descending confidence).
func dedupeNodeHostCandidates(in []nodeHostCandidate) []nodeHostCandidate {
	seen := map[string]bool{}
	out := make([]nodeHostCandidate, 0, len(in))
	for _, c := range in {
		if c.Value == "" || seen[c.Value] {
			continue
		}
		seen[c.Value] = true
		out = append(out, c)
	}
	return out
}
