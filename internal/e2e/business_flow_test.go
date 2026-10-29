//go:build e2e

package e2e_test

import (
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"testing"
	"time"
)

const dealer = "/v1/dealerships/d-001"

// today is the business date of d-001 (Europe/London), as the API computes it. The test
// reads it once: a run that crosses London midnight could otherwise compare ages taken on
// two different days.
func today(t *testing.T) time.Time {
	t.Helper()
	loc, err := time.LoadLocation("Europe/London")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().In(loc)
	return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
}

func daysAgo(businessDate time.Time, n int) string {
	return businessDate.AddDate(0, 0, -n).Format("2006-01-02")
}

func ids(r response) []string {
	var out []string
	for _, v := range r.body["data"].([]interface{}) {
		out = append(out, v.(map[string]interface{})["vehicle_id"].(string))
	}
	return out
}

// TestBusinessFlow covers the Keyloop scenario end to end: stock, aging boundary,
// filters, pagination, the action lifecycle, sale, propagation and error mapping.
func TestBusinessFlow(t *testing.T) {
	c := newClient(t)
	bd := today(t)
	tag := randomTag(t)
	vehicleMake := "E2E-" + tag
	stock := func(id, model string, age int) {
		c.expect(c.do("POST", dealer+"/vehicles", map[string]interface{}{
			"vehicle_id": id,
			"vin":        "1HGCM82633A004352",
			"make":       vehicleMake,
			"model":      model,
			"model_year": 2023,
			"stocked_on": daysAgo(bd, age),
		}, nil), http.StatusCreated, "")
	}
	v120, v91, v90, v89 := "e2e-"+tag+"-120", "e2e-"+tag+"-91", "e2e-"+tag+"-90", "e2e-"+tag+"-89"

	start := time.Now()
	stock(v120, "Corolla", 120)
	listOwn := func(c *client, extra string) response {
		return c.expect(c.do("GET", dealer+"/vehicles?make="+url.QueryEscape(vehicleMake)+extra, nil, nil), http.StatusOK, "")
	}
	lag := c.eventually("the first stocked vehicle", func() bool { return len(ids(listOwn(c, ""))) == 1 })
	t.Logf("stock → visible in the read model after %s (command started %s ago)", lag.Round(time.Millisecond), time.Since(start).Round(time.Millisecond))
	stock(v91, "Corolla", 91)
	stock(v90, "Yaris", 90)
	stock(v89, "Yaris", 89)
	c.eventually("all four vehicles", func() bool { return len(ids(listOwn(c, ""))) == 4 })

	t.Run("aging boundary: more than 90 days, exactly 90 is not aging", func(t *testing.T) {
		c := c.with(t)
		if got := ids(listOwn(c, "&aging=true")); !reflect.DeepEqual(got, []string{v120, v91}) {
			t.Fatalf("aging list %v", got)
		}
		d := c.expect(c.do("GET", dealer+"/vehicles/"+v90, nil, nil), http.StatusOK, "")
		if d.body["age_days"] != 90.0 || d.body["is_aging"] != false {
			t.Fatalf("90-day vehicle: %s", d.rawBody)
		}
		tomorrow := bd.AddDate(0, 0, 1).Format("2006-01-02")
		if got := ids(listOwn(c, "&aging=true&as_of="+tomorrow)); len(got) != 3 {
			t.Fatalf("aging list as of tomorrow %v, want the 90-day vehicle to have turned", got)
		}
	})

	t.Run("filters", func(t *testing.T) {
		c := c.with(t)
		if got := ids(listOwn(c, "&model=yaris")); !reflect.DeepEqual(got, []string{v90, v89}) {
			t.Fatalf("model filter (case-insensitive) %v", got)
		}
		if got := ids(listOwn(c, "&min_age_days=90&max_age_days=91")); !reflect.DeepEqual(got, []string{v91, v90}) {
			t.Fatalf("age range %v", got)
		}
		if got := ids(listOwn(c, "&sort=age_asc")); !reflect.DeepEqual(got, []string{v89, v90, v91, v120}) {
			t.Fatalf("age_asc %v", got)
		}
	})

	t.Run("keyset pagination", func(t *testing.T) {
		c := c.with(t)
		first := listOwn(c, "&limit=3")
		page := first.body["page"].(map[string]interface{})
		if page["has_more"] != true || page["next_cursor"] == nil {
			t.Fatalf("first page %s", first.rawBody)
		}
		second := listOwn(c, "&limit=3&cursor="+url.QueryEscape(page["next_cursor"].(string)))
		all := append(ids(first), ids(second)...)
		if !reflect.DeepEqual(all, []string{v120, v91, v90, v89}) {
			t.Fatalf("pages %v", all)
		}
		c.expect(c.do("GET", dealer+"/vehicles?make=other&cursor="+url.QueryEscape(page["next_cursor"].(string)), nil, nil),
			http.StatusBadRequest, "INVALID_CURSOR")
	})

	t.Run("action lifecycle and sale", func(t *testing.T) {
		c := c.with(t)
		vehicle := dealer + "/vehicles/" + v120
		c.expect(c.do("POST", dealer+"/vehicles/"+v89+"/actions", map[string]string{"action_type": "HOLD"}, nil),
			http.StatusConflict, "VEHICLE_NOT_AGING")
		proposed := c.expect(c.do("POST", vehicle+"/actions", map[string]string{"action_type": "PRICE_REDUCTION", "note": "5%"},
			map[string]string{"If-Match": `"1"`}), http.StatusCreated, "")
		action := proposed.body["current_action"].(map[string]interface{})["action_id"].(string)
		if proposed.header.Get("Location") != vehicle+"/actions/"+action || proposed.header.Get("ETag") != `"2"` {
			t.Fatalf("propose headers %v", proposed.header)
		}
		c.expect(c.do("POST", vehicle+"/actions", map[string]string{"action_type": "HOLD"}, nil), http.StatusConflict, "ACTION_ALREADY_ACTIVE")
		c.expect(c.do("PUT", vehicle+"/actions/"+action, map[string]string{"action_type": "AUCTION", "note": "lot 7"},
			map[string]string{"If-Match": `"1"`}), http.StatusPreconditionFailed, "VERSION_MISMATCH")
		c.expect(c.do("PUT", vehicle+"/actions/"+action, map[string]string{"action_type": "AUCTION", "note": "lot 7"},
			map[string]string{"If-Match": `"2"`}), http.StatusOK, "")
		c.expect(c.do("POST", vehicle+"/actions/"+action+"/cancel", map[string]string{"reason": "buyer found"}, nil), http.StatusOK, "")
		c.expect(c.do("POST", vehicle+"/actions", map[string]string{"action_type": "HOLD", "note": "hold for buyer"}, nil), http.StatusCreated, "")
		sold := c.expect(c.do("POST", vehicle+"/sale", map[string]string{"sold_on": bd.Format("2006-01-02")}, nil), http.StatusOK, "")
		if sold.body["status"] != "SOLD" || sold.body["current_action"] != nil || sold.body["version"] != 6.0 {
			t.Fatalf("sale %s", sold.rawBody)
		}
		c.expect(c.do("POST", vehicle+"/sale", map[string]string{"sold_on": bd.Format("2006-01-02")}, nil), http.StatusConflict, "VEHICLE_ALREADY_SOLD")

		c.eventually("the sale", func() bool {
			return c.do("GET", vehicle, nil, nil).body["status"] == "SOLD"
		})
		history := c.expect(c.do("GET", vehicle+"/actions", nil, nil), http.StatusOK, "")
		var changes []string
		for _, e := range history.body["data"].([]interface{}) {
			entry := e.(map[string]interface{})
			changes = append(changes, fmt.Sprint(entry["change"], "/", entry["actor_id"]))
		}
		want := []string{"PROPOSED/e2e-manager", "REVISED/e2e-manager", "CANCELLED/e2e-manager", "PROPOSED/e2e-manager", "CLOSED_BY_SALE/e2e-manager"}
		if !reflect.DeepEqual(changes, want) {
			t.Fatalf("history %v", changes)
		}
		if got := ids(listOwn(c, "")); len(got) != 3 {
			t.Fatalf("in-stock list after the sale %v", got)
		}
	})

	t.Run("error mapping", func(t *testing.T) {
		c := c.with(t)
		c.expect(c.do("GET", "/v1/dealerships/d-999/inventory/summary", nil, nil), http.StatusNotFound, "DEALERSHIP_NOT_FOUND")
		c.expect(c.do("GET", dealer+"/vehicles/missing-"+tag, nil, nil), http.StatusNotFound, "VEHICLE_NOT_FOUND")
		c.expect(c.do("GET", dealer+"/vehicles?agng=true", nil, nil), http.StatusBadRequest, "INVALID_QUERY")
		c.expect(c.do("POST", dealer+"/vehicles", map[string]interface{}{"vin": "X"}, map[string]string{"X-Actor-Id": ""}),
			http.StatusBadRequest, "ACTOR_REQUIRED")
		bad := c.expect(c.do("POST", dealer+"/vehicles", map[string]interface{}{
			"vin":        "INVALID",
			"make":       vehicleMake,
			"model":      "x",
			"model_year": 2023,
			"stocked_on": daysAgo(bd, 1),
		}, nil), http.StatusBadRequest, "VALIDATION_ERROR")
		if bad.body["request_id"] == "" || bad.header.Get("X-Request-Id") == "" {
			t.Fatalf("problem without request ID: %s", bad.rawBody)
		}
		c.expect(c.do("POST", dealer+"/vehicles/"+v91+"/sale", map[string]string{"sold_on": daysAgo(bd, 200)}, nil),
			http.StatusBadRequest, "VALIDATION_ERROR")
	})

	t.Run("summary", func(t *testing.T) {
		c := c.with(t)
		s := c.expect(c.do("GET", dealer+"/inventory/summary", nil, nil), http.StatusOK, "")
		if s.body["aging_threshold_days"] != 90.0 || s.body["aging"].(float64) < 1 {
			t.Fatalf("summary %s", s.rawBody)
		}
	})
}
