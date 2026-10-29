//go:build integration && pipeline

package pipeline_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// connectorConfig is the shipped connector config with this test's database, names
// and a unique server id.
func (e *env) connectorConfig(t *testing.T) map[string]string {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(file), "../../deploy/debezium/es-event-connector.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]string
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	db := e.mysqlCfg.DBName
	table := db + ".es_event"
	cfg["database.hostname"] = envOr("CDC_TEST_MYSQL_HOST", "mysql")
	cfg["database.user"] = envOr("DEBEZIUM_MYSQL_USER", "debezium")
	cfg["database.server.id"] = fmt.Sprint(200000 + time.Now().UnixNano()%1000000000)
	cfg["topic.prefix"] = e.name
	cfg["database.include.list"] = db
	cfg["table.include.list"] = table
	cfg["message.key.columns"] = table + ":aggregate_id"
	cfg["snapshot.select.statement.overrides"] = table
	delete(cfg, "snapshot.select.statement.overrides.inventory.es_event")
	cfg["snapshot.select.statement.overrides."+table] = "SELECT * FROM " + table + " ORDER BY aggregate_id, version"
	cfg["schema.history.internal.kafka.topic"] = e.name + ".schema-history"
	return cfg
}

// registerConnector creates (or updates) the test's connector and waits until its
// task runs. The connector is deleted when the test ends.
func (e *env) registerConnector(t *testing.T) {
	t.Helper()
	body, _ := json.Marshal(e.connectorConfig(t))
	e.connect(t, http.MethodPut, "/connectors/"+e.connector+"/config", body, http.StatusOK, http.StatusCreated)
	t.Cleanup(func() {
		e.connect(t, http.MethodDelete, "/connectors/"+e.connector, nil, http.StatusNoContent, http.StatusNotFound)
	})
	e.waitConnectorRunning(t)
}

func (e *env) waitConnectorRunning(t *testing.T) {
	t.Helper()
	waitFor(t, "connector task RUNNING", 60*time.Second, func() bool {
		var status struct {
			Connector struct{ State string }
			Tasks     []struct{ State, Trace string }
		}
		raw := e.connect(t, http.MethodGet, "/connectors/"+e.connector+"/status", nil, http.StatusOK, http.StatusNotFound)
		if err := json.Unmarshal(raw, &status); err != nil || len(status.Tasks) == 0 {
			return false
		}
		if status.Tasks[0].State == "FAILED" {
			t.Fatalf("connector task failed: %.800s", status.Tasks[0].Trace)
		}
		return status.Connector.State == "RUNNING" && status.Tasks[0].State == "RUNNING"
	})
}

func (e *env) connect(t *testing.T, method, path string, body []byte, okStatus ...int) []byte {
	t.Helper()
	req, err := http.NewRequest(method, e.connectURL+path, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	for _, s := range okStatus {
		if resp.StatusCode == s {
			return raw
		}
	}
	t.Fatalf("%s %s: %d %s", method, path, resp.StatusCode, raw)
	return nil
}
