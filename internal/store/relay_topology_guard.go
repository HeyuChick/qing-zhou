package store

import (
	"database/sql"
	"errors"
	"fmt"

	"qingzhou/internal/singbox"
)

// ErrRelayTopology identifies a rejected edit, not a deployment failure. The
// transaction has not committed and callers can safely show its reason.
var ErrRelayTopology = errors.New("中转配置未保存")

func relayTopologyError(format string, args ...any) error {
	return fmt.Errorf("%w：%s", ErrRelayTopology, fmt.Sprintf(format, args...))
}

func relayMeteringSettingsWith(tx *sql.Tx) (links, users bool, err error) {
	var p0, p1 string
	err = tx.QueryRow(`SELECT
		COALESCE((SELECT value FROM settings WHERE key=?),''),
		COALESCE((SELECT value FROM settings WHERE key=?),'')`, RelayMeteringSetting, RelayUserMeteringSetting).Scan(&p0, &p1)
	return p0 == "true", p0 == "true" && p1 == "true", err
}

// validateRelayTopologySave checks the candidate snapshot after the edit and
// before COMMIT. BEGIN IMMEDIATE serializes competing edits and setting changes,
// so two individually valid edits cannot jointly commit a cycle or dangling edge.
// It intentionally does not use runtime capability observations (including Vision
// freshness), generate identities, or prepare deployments. Opt-out keeps the
// legacy behavior; an edit that repairs an old invalid graph is allowed.
func (s *Store) validateRelayTopologySave(tx *sql.Tx) error {
	links, users, err := relayMeteringSettingsWith(tx)
	if err != nil {
		return err
	}
	return s.validateRelayTopologyForSettings(tx, links, users)
}

// validateRelayTopologyForSettings is the read-only static preflight shared by
// saved edits and candidate switch changes. It never writes settings or requires
// runtime capability observations. Callers own the snapshot transaction.
func (s *Store) validateRelayTopologyForSettings(tx *sql.Tx, links, users bool) error {
	if !links {
		return nil
	}
	type edge struct {
		from, to int64
		route    int64
		tag      string
	}
	// Match the planner's physical and logical edge selection. A LEFT JOIN also
	// catches a new logical route with a missing entry, rather than hiding it.
	rows, err := tx.Query(`SELECT id,upstream_inbound_id,0,tag FROM sb_inbounds WHERE enabled=1 AND upstream_inbound_id<>0
		UNION ALL SELECT COALESCE(i.id,0),n.route_upstream_inbound_id,n.id,n.inbound_tag
		FROM nodes n LEFT JOIN sb_inbounds i ON i.tag=n.inbound_tag
		WHERE n.enabled=1 AND n.type='self_built' AND n.route_upstream_broken=0
		AND n.route_upstream_inbound_id<>0 AND (i.id IS NULL OR i.enabled=1)
		ORDER BY 1,3`)
	if err != nil {
		return err
	}
	var edges []edge
	for rows.Next() {
		var e edge
		if err = rows.Scan(&e.from, &e.to, &e.route, &e.tag); err != nil {
			rows.Close()
			return err
		}
		edges = append(edges, e)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err = rows.Close(); err != nil {
		return err
	}

	inbounds := map[int64]*SbInbound{}
	load := func(id int64) (*SbInbound, error) {
		if ib, ok := inbounds[id]; ok {
			return ib, nil
		}
		ib, err := meteringInboundWith(tx, id)
		if err == nil {
			inbounds[id] = ib
		}
		return ib, err
	}
	checked := map[int64]bool{}
	checkEndpoint := func(ib *SbInbound, render bool) error {
		if !render {
			if ib.ServerID == LocalNodeID {
				return nil
			}
			var enabled bool
			err := tx.QueryRow(`SELECT enabled FROM servers WHERE id=?`, ib.ServerID).Scan(&enabled)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			if err != nil || !enabled {
				return relayTopologyError("入站 %s 所在服务器不存在或已禁用，请先停用或改接使用它的中转线路", ib.Tag)
			}
			return nil
		}
		if checked[ib.ID] {
			return nil
		}
		_, server, tls, err := s.relayTargetSpecWith(tx, ib)
		if err != nil {
			return relayTopologyError("入站 %s 不可用：%v", ib.Tag, err)
		}
		probe := singbox.User{UUID: "00000000-0000-4000-8000-000000000001", Password: "protocol-preflight-only"}
		if _, err := s.relayOutboundWithIdentity(ib, map[int64]*Server{ib.ServerID: server}, map[int64]*SbTls{ib.TlsID: tls}, &probe, "protocol-preflight-only"); err != nil {
			return relayTopologyError("入站 %s 的协议配置不可用：%v", ib.Tag, err)
		}
		checked[ib.ID] = true
		return nil
	}
	graph := map[int64][]int64{}
	for _, e := range edges {
		from, err := load(e.from)
		if err != nil {
			return err
		}
		to, err := load(e.to)
		if err != nil {
			return err
		}
		if from == nil {
			return relayTopologyError("逻辑线路 %d 的入口 %s 不存在", e.route, e.tag)
		}
		if to == nil || !to.Enabled {
			return relayTopologyError("入口 %s 引用的落地入站 %d 不存在或已禁用，请先停用或改接引用它的线路", from.Tag, e.to)
		}
		if e.route != 0 && from.Type == "mixed" {
			return relayTopologyError("逻辑线路 %d 的入口 %s 使用 Mixed，暂不支持按逻辑线路分流，请使用独立入站", e.route, from.Tag)
		}
		if from.ServerID == to.ServerID {
			return relayTopologyError("入口 %s 与落地 %s 位于同一服务器，链路计量暂不支持同机多跳", from.Tag, to.Tag)
		}
		if users {
			if err := supportedUserMeteringEdge(from, to); err != nil {
				return relayTopologyError("%v", err)
			}
		} else if err := supportedMeteringLanding(to); err != nil {
			return relayTopologyError("%v", err)
		}
		if err := checkEndpoint(from, users && from.Type != "mixed"); err != nil {
			return err
		}
		if err := checkEndpoint(to, true); err != nil {
			return err
		}
		graph[from.ServerID] = append(graph[from.ServerID], to.ServerID)
	}
	color := map[int64]int{}
	var visit func(int64) bool
	visit = func(id int64) bool {
		if color[id] != 0 {
			return color[id] == 2
		}
		color[id] = 1
		for _, target := range graph[id] {
			if !visit(target) {
				return false
			}
		}
		color[id] = 2
		return true
	}
	for id := range graph {
		if !visit(id) {
			return relayTopologyError("活动中转线路存在服务器环路，请先断开或改接相关线路")
		}
	}
	return nil
}

// Refuse a destructive implicit direct-exit conversion while metering is on.
// Disabled references retain the legacy cleanup behavior and can be repaired
// before re-enabling. Explicitly clearing a route via Save is still supported.
func (s *Store) guardRelayInboundDelete(tx *sql.Tx, id int64) error {
	links, _, err := relayMeteringSettingsWith(tx)
	if err != nil || !links {
		return err
	}
	var count int
	err = tx.QueryRow(`SELECT
		(SELECT COUNT(*) FROM sb_inbounds WHERE enabled=1 AND upstream_inbound_id=?) +
		(SELECT COUNT(*) FROM nodes n JOIN sb_inbounds i ON i.tag=n.inbound_tag
		 WHERE n.enabled=1 AND n.type='self_built' AND n.route_upstream_broken=0
		 AND i.enabled=1 AND n.route_upstream_inbound_id=?)`, id, id).Scan(&count)
	if err != nil {
		return err
	}
	if count != 0 {
		return relayTopologyError("落地入站 %d 仍被 %d 条活动中转线路引用，请先停用或改接线路再删除", id, count)
	}
	return nil
}
