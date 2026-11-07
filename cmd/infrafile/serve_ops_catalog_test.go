package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestOpsCatalogDenominatorAndExactDeploymentScopes(t *testing.T) {
	f, dir, _ := actorFixture(t, "ok")
	f.login(t)
	o := readOps(t, f)
	if len(o.Operations) != len(o.Servers)+len(o.Deployments) {
		t.Fatal("operation catalog lost live objects")
	}
	cap := o.ActionCapabilities[0]
	for _, row := range o.Operations {
		if len(row.ActionIDs) > 0 && (row.Host != cap.Target || row.Kind == "deployment" && !containsOpsKey(cap.RelatedKeys, row.Object)) {
			t.Fatal("action leaked across host/application")
		}
	}
	if countAction(dir, "plan") != 0 || countAction(dir, "apply") != 0 || countAction(dir, "status") != 0 {
		t.Fatal("catalog executed an adapter")
	}
	// The same app on another machine gets no first-host control grant.
	other := o.Deployments[0]
	other.ID = "second-deployment"
	other.Host = o.Servers[1].Key
	o.Deployments = append(o.Deployments, other)
	rows := opsOperationCatalog(&o)
	last := rows[len(rows)-1]
	if len(last.ActionIDs) != 0 || len(last.ReadIDs) != 0 {
		t.Fatal("app-wide related key widened the host scope")
	}
}

func TestOpsCatalogSeparatesObserverFromControllerAndSharedDependencies(t *testing.T) {
	_, o, _ := opsFixture(t)
	d := &o.Deployments[0]
	d.Criteria = []opsCriterion{
		{ID: "service", Component: "worker", Kind: "fleet_systemd", Selector: "worker.service", Observer: d.Host, DataMode: "stateless", WorkloadMode: "service"},
		{ID: "container-running", Component: "store", Kind: "fleet_container_running", Selector: "store", Observer: d.Host, DataMode: "stateful", WorkloadMode: "service"},
		{ID: "container-health", Component: "store", Kind: "fleet_container_health", Selector: "store", Observer: d.Host, DataMode: "stateful", WorkloadMode: "service"},
		{ID: "timer", Component: "worker", Kind: "systemd_timer", Selector: "worker.timer", Observer: d.Host, DataMode: "stateless", WorkloadMode: "scheduled"},
		{ID: "remote-observer", Component: "observer", Kind: "fleet_systemd", Selector: "observer.service", Observer: o.Servers[1].Key, DataMode: "stateless", WorkloadMode: "service"},
	}
	d.Resources = []string{"Resource/shared"}
	o.Deployments[1].Resources = []string{"Resource/shared"}
	o.Deployments[1].Criteria = []opsCriterion{{ID: "other-store", Component: "data", Kind: "fleet_container_running", Selector: "store", Observer: d.Host, DataMode: "stateful", WorkloadMode: "service"}}
	rows := opsOperationCatalog(&o)
	row := rows[len(o.Servers)]
	if len(row.Controllers) != 3 || row.Boundary != "stateful_control_not_bound" || !containsOpsKey(row.Prerequisites, "schedule_mutex_and_cost_limits") || !containsOpsKey(row.Prerequisites, "roles_replication_and_restore_evidence") {
		t.Fatal(row)
	}
	if len(row.SharedWith) != 1 || row.SharedWith[0] != o.Deployments[1].Application {
		t.Fatal("shared dependency attribution lost")
	}
	if len(row.SharedControllersWith) != 1 || row.SharedControllersWith[0] != o.Deployments[1].Application {
		t.Fatal("same control object was hidden behind app names")
	}
	for _, c := range row.Controllers {
		if c.Name == "observer.service" {
			t.Fatal("observation host became deployment controller")
		}
		if c.Name == "store" && len(c.Checks) != 2 {
			t.Fatal("container identity was duplicated or evidence collapsed")
		}
	}
	o.Deployments[1].Host = o.Servers[1].Key
	o.Deployments[1].Criteria[0].Observer = o.Servers[1].Key
	separate := opsOperationCatalog(&o)[len(o.Servers)]
	if len(separate.SharedControllersWith) != 0 || len(separate.SharedWith) != 1 {
		t.Fatal("same container name on another machine became a shared controller")
	}
}

func TestOpsCatalogCannotBeInjectedThroughAttachment(t *testing.T) {
	f, o, path := opsFixture(t)
	o.Operations = []opsOperationRow{{ID: "fake", ActionIDs: []string{"restart"}}}
	writeOps(t, path, o)
	f.login(t)
	if out := f.send(f.request("POST", "/api/ops-overview", "{}")); out.Code != 422 {
		t.Fatal("untrusted attachment supplied operation grants", out.Code)
	}
}

func TestOpsCatalogIncludesCrossHostResourceConsumersWithoutWideningGrants(t *testing.T) {
	o := opsOverview{
		Servers: []opsServer{{Key: "Server/data", Applications: []string{"App/local"}}, {Key: "Server/replica"}, {Key: "Server/web"}},
		Resources: []opsResource{
			{Key: "Resource/unused", Hosts: []string{"Server/data"}},
			{Key: "Resource/shared", Hosts: []string{"Server/replica", "Server/data", "Server/data"}},
			{Key: "Resource/cloud"},
		},
		Deployments: []opsDeployment{
			{ID: "remote-two", Application: "App/remote", Host: "Server/web", Resources: []string{"Resource/shared", "Resource/shared"}},
			{ID: "local", Application: "App/local", Host: "Server/data", Resources: []string{"Resource/shared"}},
			{ID: "remote-one", Application: "App/remote", Host: "Server/replica", Resources: []string{"Resource/shared"}},
			{ID: "unrelated", Application: "App/unrelated", Host: "Server/web", Resources: []string{"Resource/cloud"}},
		},
		ActionCapabilities: []opsReadCapability{{ID: "fixed", Target: "Server/data", RelatedKeys: []string{"App/local"}}},
	}
	rows := opsOperationCatalog(&o)
	data, replica, web := rows[0], rows[1], rows[2]
	if len(data.HostedResources) != 2 || data.HostedResources[0].Key != "Resource/shared" || data.HostedResources[1].Key != "Resource/unused" || len(data.HostedResources[1].Consumers) != 0 {
		t.Fatal("hosted resources lost, duplicated or falsely treated as unused", data.HostedResources)
	}
	consumers := data.HostedResources[0].Consumers
	if len(consumers) != 3 || consumers[0].Deployment != "local" || consumers[1].Deployment != "remote-one" || consumers[2].Deployment != "remote-two" || consumers[2].Host != "Server/web" {
		t.Fatal("consumer deployment identities were collapsed or inferred", consumers)
	}
	if len(replica.HostedResources) != 1 || len(replica.HostedResources[0].Consumers) != 3 || len(web.HostedResources) != 0 {
		t.Fatal("multi-node or hostless resource attribution is wrong")
	}
	if len(data.Applications) != 1 || data.Applications[0] != "App/local" || len(data.ActionIDs) != 1 || len(replica.ActionIDs) != 0 || len(web.ActionIDs) != 0 {
		t.Fatal("resource consumers became local applications or control grants")
	}
	for _, row := range rows[len(o.Servers):] {
		if row.Object != "App/local" && len(row.ActionIDs) != 0 {
			t.Fatal("resource usage granted a dependent application an action")
		}
	}
}

func TestOpsProviderRejectsRelatedAppWithoutDeploymentOnTarget(t *testing.T) {
	f, _, _ := actorFixture(t, "ok")
	raw, _ := os.ReadFile(f.app.opsFile)
	var overview opsOverview
	json.Unmarshal(raw, &overview)
	app := f.app.opsActor.config.Capabilities[0].RelatedKeys[0]
	for i := range overview.Deployments {
		if overview.Deployments[i].Application == app {
			overview.Deployments[i].Host = overview.Servers[1].Key
		}
	}
	writeOps(t, f.app.opsFile, overview)
	config := f.app.opsActor.config
	model, _ := os.ReadFile(f.app.opsFile)
	digest := sha256.Sum256(model)
	config.OverviewSHA256 = hex.EncodeToString(digest[:])
	raw, _ = json.Marshal(config)
	path := filepath.Join(t.TempDir(), "actor.json")
	os.WriteFile(path, raw, 0600)
	if _, err := f.app.loadOpsProvider(context.Background(), path, "infrafile-ops-actor/v1", 45); err == nil {
		t.Fatal("related app did not belong to target host")
	}
}

func TestOpsProviderCannotExposeAnotherHostThroughRelatedKeys(t *testing.T) {
	f, dir, _ := actorFixture(t, "ok")
	value, err := f.app.opsOverview(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	view := value.(opsOverview)
	config := f.app.opsActor.config
	config.Capabilities = append([]opsReadCapability{}, config.Capabilities...)
	config.Capabilities[0].RelatedKeys = []string{view.Servers[1].Key}
	raw, _ := json.Marshal(config)
	path := filepath.Join(t.TempDir(), "actor.json")
	os.WriteFile(path, raw, 0600)
	if _, err = f.app.loadOpsProvider(context.Background(), path, "infrafile-ops-actor/v1", 45); err == nil {
		t.Fatal("related keys exposed one host's action on another host")
	}
	if countAction(dir, "plan") != 0 || countAction(dir, "apply") != 0 {
		t.Fatal("relation validation executed a command")
	}
}
