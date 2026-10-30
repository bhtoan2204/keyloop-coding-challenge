//go:build integration

package presentation_test

import (
	"context"
	"reflect"
	"testing"
)

// TestReadAfterWriteIsImmediate: events and read model commit in one transaction, so
// after every successful command a GET (as the read-only role) already shows that
// version, the projected version equals the aggregate version, and the history rows
// carry the database-generated ids of the events they came from. A rebuild from the
// event store reproduces the same read model.
func TestReadAfterWriteIsImmediate(t *testing.T) {
	m := newPostgresAPI(t, nil)
	get := func(path string) reply {
		t.Helper()
		return do(t, m.srv, call{method: "GET", path: path})
	}
	count := func(r reply) int { return len(r.body["data"].([]interface{})) }
	// consistent checks the committed write side against the read model after a command.
	consistent := func(id string, response reply) {
		t.Helper()
		want := response.body["version"]
		if d := get(vehicles + "/" + id); d.status != 200 || d.body["version"] != want || d.header.Get("ETag") != response.header.Get("ETag") {
			t.Fatalf("GET after the command: %d %s, command answered version %v", d.status, d.rawBody, want)
		}
		events := m.events(t, id)
		version, history := m.projected(t, id)
		if version != len(events) || float64(version) != want.(float64) {
			t.Fatalf("aggregate has %d events, projected version %d, command version %v", len(events), version, want)
		}
		ids := make(map[int]int64, len(events))
		for _, e := range events {
			ids[e.Version] = e.ID
		}
		for _, h := range history {
			if ids[h.Version] != h.EventID {
				t.Fatalf("history v%d has event_id %d, the stored event has id %d", h.Version, h.EventID, ids[h.Version])
			}
		}
	}

	for _, v := range []struct{ id, stockedOn string }{{"old", "2026-07-01"}, {"young", "2026-10-01"}} {
		r := do(t, m.srv, call{method: "POST", path: vehicles, body: stockBody(v.id, v.stockedOn)})
		if r.status != 201 {
			t.Fatalf("stock: %d %s", r.status, r.rawBody)
		}
		consistent(v.id, r)
	}
	if list := get(vehicles); count(list) != 2 || list.body["data"].([]interface{})[0].(map[string]interface{})["vehicle_id"] != "old" {
		t.Fatalf("list right after stocking: %s", list.rawBody)
	}
	if aging := get(vehicles + "?aging=true"); count(aging) != 1 {
		t.Fatalf("aging: %s", aging.rawBody)
	}

	r := do(t, m.srv, call{method: "POST", path: vehicles + "/old/actions", body: `{"action_type":"PRICE_REDUCTION","note":"5%"}`,
		headers: map[string]string{"X-Request-Id": "req-atomic"}})
	if r.status != 201 {
		t.Fatalf("propose: %d %s", r.status, r.rawBody)
	}
	consistent("old", r)
	actionID := r.body["current_action"].(map[string]interface{})["action_id"].(string)
	history := get(vehicles + "/old/actions")
	entry := history.body["data"].([]interface{})[0].(map[string]interface{})
	if count(history) != 1 || entry["change"] != "PROPOSED" || entry["correlation_id"] != "req-atomic" || entry["actor_id"] != "manager-42" {
		t.Fatalf("history right after propose: %s", history.rawBody)
	}

	for _, c := range []call{
		{method: "PUT", path: vehicles + "/old/actions/" + actionID, body: `{"action_type":"AUCTION","note":"lot 7"}`},
		{method: "POST", path: vehicles + "/old/actions/" + actionID + "/cancel", body: `{"reason":"buyer found"}`},
		{method: "POST", path: vehicles + "/old/actions", body: `{"action_type":"HOLD"}`},
		{method: "POST", path: vehicles + "/old/sale", body: `{"sold_on":"2026-10-29"}`},
	} {
		r := do(t, m.srv, c)
		if r.status != 200 && r.status != 201 {
			t.Fatalf("%s %s: %d %s", c.method, c.path, r.status, r.rawBody)
		}
		consistent("old", r)
	}
	if n := count(get(vehicles + "?aging=true")); n != 0 {
		t.Fatalf("sold vehicle still aging: %d", n)
	}
	sum := get("/v1/dealerships/d-001/inventory/summary")
	if sum.body["in_stock"] != 1.0 || sum.body["aging"] != 0.0 {
		t.Fatalf("summary: %s", sum.rawBody)
	}
	changes := []string{}
	for _, e := range get(vehicles + "/old/actions").body["data"].([]interface{}) {
		changes = append(changes, e.(map[string]interface{})["change"].(string))
	}
	if !reflect.DeepEqual(changes, []string{"PROPOSED", "REVISED", "CANCELLED", "PROPOSED", "CLOSED_BY_SALE"}) {
		t.Fatalf("history %v", changes)
	}

	before := get(vehicles+"?status=ALL").rawBody + get(vehicles+"/old/actions").rawBody
	if res, err := m.rebuild.Rebuild(context.Background()); err != nil || res.Read != 7 || res.Applied != 7 {
		t.Fatalf("rebuild: %+v %v", res, err)
	}
	if after := get(vehicles+"?status=ALL").rawBody + get(vehicles+"/old/actions").rawBody; after != before {
		t.Fatalf("rebuild changed the read model\nbefore %s\nafter  %s", before, after)
	}
}
