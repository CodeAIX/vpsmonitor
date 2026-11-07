package hub

import (
	"context"
	"encoding/json"
	"strconv"
	"time"

	"github.com/CodeAIX/vpsmonitor/internal/hub/systems"
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
)

func historyStateView(record *core.Record) any {
	if record == nil {
		return nil
	}
	return map[string]any{"supported": record.GetBool("supported"), "retention_seconds": record.GetInt64("retention_seconds"), "cursor": record.GetInt64("cursor"), "oldest": record.GetInt64("oldest"), "newest": record.GetInt64("newest"), "last_sync": record.GetInt64("last_sync"), "cache_samples": record.GetInt("cache_samples"), "cache_bytes": record.GetInt64("cache_bytes"), "limited": record.GetBool("limited"), "error": record.GetString("error"), "gap_from": record.GetInt64("gap_from"), "gap_to": record.GetInt64("gap_to")}
}

func (h *Hub) historyStatus(e *core.RequestEvent) error {
	e.Response.Header().Set("Cache-Control", "no-store")
	sys, err := authorizedTrafficSystem(e)
	if err != nil {
		return err
	}
	state, err := firstTrafficRecord(e.App, "agent_history_state", sys.Id)
	if err != nil {
		return err
	}
	return e.JSON(200, map[string]any{"state": historyStateView(state)})
}

func (h *Hub) historyStats(e *core.RequestEvent) error {
	e.Response.Header().Set("Cache-Control", "no-store")
	sys, err := authorizedTrafficSystem(e)
	if err != nil {
		return err
	}
	q := e.Request.URL.Query()
	kind := q.Get("type")
	minutes, ok := systems.HistoryPeriods[kind]
	if !ok {
		return e.BadRequestError("Invalid history resolution", nil)
	}
	from, err := strconv.ParseInt(q.Get("from"), 10, 64)
	if err != nil {
		return e.BadRequestError("Invalid history start", nil)
	}
	to, err := strconv.ParseInt(q.Get("to"), 10, 64)
	if err != nil || from < 0 || to <= from || to > time.Now().Add(5*time.Minute).UnixMilli() || to-from > minutes*60000*600 {
		return e.BadRequestError("History range exceeds 600 chart buckets", nil)
	}
	ctx, cancel := context.WithTimeout(e.Request.Context(), 5*time.Second)
	defer cancel()
	state, err := firstTrafficRecord(e.App, "agent_history_state", sys.Id)
	if err != nil {
		return err
	}
	type point struct {
		Created    int64           `json:"created"`
		Bucket     int64           `json:"bucket"`
		Interval   int64           `json:"interval"`
		Samples    int             `json:"samples"`
		Complete   bool            `json:"complete"`
		Stats      json.RawMessage `json:"stats"`
		Containers json.RawMessage `json:"containers"`
	}
	var rows []*core.Record
	width := minutes * 60000
	err = e.App.RecordQuery("agent_history_records").WithContext(ctx).AndWhere(dbx.NewExp("system={:id} AND type={:type} AND stamp >= {:from} AND stamp < {:to}", dbx.Params{"id": sys.Id, "type": kind, "from": from / width * width, "to": to})).OrderBy("stamp").Limit(601).All(&rows)
	if err != nil {
		return err
	}
	points := make([]point, 0, len(rows))
	size := 0
	for _, r := range rows {
		p := point{Created: r.GetInt64("last"), Bucket: r.GetInt64("stamp"), Interval: width, Samples: r.GetInt("samples"), Complete: r.GetInt64("samples") == minutes}
		if p.Created < from || p.Created >= to {
			continue
		}
		if err := r.UnmarshalJSONField("stats", &p.Stats); err != nil {
			return err
		}
		if err := r.UnmarshalJSONField("containers", &p.Containers); err != nil {
			return err
		}
		size += len(p.Stats) + len(p.Containers)
		if size > 8*1024*1024 {
			return e.JSON(413, map[string]string{"message": "History chart payload exceeds limit"})
		}
		points = append(points, p)
	}
	return e.JSON(200, map[string]any{"state": historyStateView(state), "points": points})
}
