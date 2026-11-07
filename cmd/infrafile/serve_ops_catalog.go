package main

import "sort"

// This catalog is derived after ledger validation. It describes existing
// bindings and control gaps; it is never an executable command or a grant.
type opsController struct {
	Kind         string   `json:"kind"`
	Name         string   `json:"name"`
	Component    string   `json:"component"`
	DataMode     string   `json:"data_mode"`
	WorkloadMode string   `json:"workload_mode"`
	Checks       []string `json:"checks"`
}
type opsResourceConsumer struct {
	Application string `json:"application"`
	Host        string `json:"host"`
	Deployment  string `json:"deployment"`
}
type opsHostedResource struct {
	Key       string                `json:"key"`
	Consumers []opsResourceConsumer `json:"consumers"`
}
type opsOperationRow struct {
	ID                    string              `json:"id"`
	Kind                  string              `json:"kind"`
	Object                string              `json:"object"`
	Host                  string              `json:"host"`
	Deployment            string              `json:"deployment,omitempty"`
	Controllers           []opsController     `json:"controllers"`
	ReadIDs               []string            `json:"read_ids"`
	ActionIDs             []string            `json:"action_ids"`
	Applications          []string            `json:"applications"`
	SharedWith            []string            `json:"shared_with"`
	SharedControllersWith []string            `json:"shared_controllers_with"`
	HostedResources       []opsHostedResource `json:"hosted_resources"`
	Notice                string              `json:"notice"`
	Boundary              string              `json:"boundary"`
	Prerequisites         []string            `json:"prerequisites"`
}

func containsOpsKey(keys []string, key string) bool {
	for _, v := range keys {
		if v == key {
			return true
		}
	}
	return false
}

func opsCapabilityOwns(overview *opsOverview, cap opsReadCapability, app string) bool {
	if !containsOpsKey(cap.RelatedKeys, app) {
		return false
	}
	for _, d := range overview.Deployments {
		if d.Application == app && d.Host == cap.Target {
			return true
		}
	}
	return false
}

func opsControllers(d opsDeployment) []opsController {
	rows := []opsController{}
	for _, c := range d.Criteria {
		if c.Observer != d.Host || c.Selector == "" {
			continue
		}
		kind := map[string]string{"fleet_systemd": "systemd-service", "systemd_timer": "systemd-timer", "fleet_container_running": "docker-container", "fleet_container_health": "docker-container", "process_running": "process"}[c.Kind]
		if kind == "" {
			continue
		}
		found := false
		for i := range rows {
			r := &rows[i]
			if r.Kind == kind && r.Name == c.Selector && r.Component == c.Component && r.DataMode == c.DataMode && r.WorkloadMode == c.WorkloadMode {
				r.Checks = append(r.Checks, c.ID)
				found = true
				break
			}
		}
		if !found {
			rows = append(rows, opsController{Kind: kind, Name: c.Selector, Component: c.Component, DataMode: c.DataMode, WorkloadMode: c.WorkloadMode, Checks: []string{c.ID}})
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		return rows[i].Kind+"/"+rows[i].Name+"/"+rows[i].Component < rows[j].Kind+"/"+rows[j].Name+"/"+rows[j].Component
	})
	for i := range rows {
		sort.Strings(rows[i].Checks)
	}
	return rows
}

func opsOperationCatalog(o *opsOverview) []opsOperationRow {
	rows := []opsOperationRow{}
	type controllerKey struct{ host, kind, name string }
	controllers := map[string][]opsController{}
	owners := map[controllerKey]map[string]bool{}
	for _, d := range o.Deployments {
		controllers[d.ID] = opsControllers(d)
		for _, c := range controllers[d.ID] {
			if c.Kind == "process" {
				continue
			} // A process name is not a unique control identity.
			key := controllerKey{d.Host, c.Kind, c.Name}
			if owners[key] == nil {
				owners[key] = map[string]bool{}
			}
			owners[key][d.Application] = true
		}
	}
	for _, s := range o.Servers {
		r := opsOperationRow{ID: s.Key, Kind: "server", Object: s.Key, Host: s.Key, Controllers: []opsController{}, ReadIDs: []string{}, ActionIDs: []string{}, Applications: append([]string{}, s.Applications...), SharedWith: []string{}, SharedControllersWith: []string{}, Notice: s.Notice, Boundary: "host_control_not_bound", Prerequisites: []string{"exact_host_identity", "dependent_application_impact", "restricted_execution_identity", "reviewed_preflight_and_rollback"}}
		r.HostedResources = []opsHostedResource{}
		for _, resource := range o.Resources {
			if !containsOpsKey(resource.Hosts, s.Key) {
				continue
			}
			hosted := opsHostedResource{Key: resource.Key, Consumers: []opsResourceConsumer{}}
			for _, d := range o.Deployments {
				if containsOpsKey(d.Resources, resource.Key) {
					hosted.Consumers = append(hosted.Consumers, opsResourceConsumer{Application: d.Application, Host: d.Host, Deployment: d.ID})
				}
			}
			sort.Slice(hosted.Consumers, func(i, j int) bool {
				left, right := hosted.Consumers[i], hosted.Consumers[j]
				return left.Application+"/"+left.Host+"/"+left.Deployment < right.Application+"/"+right.Host+"/"+right.Deployment
			})
			r.HostedResources = append(r.HostedResources, hosted)
		}
		sort.Slice(r.HostedResources, func(i, j int) bool { return r.HostedResources[i].Key < r.HostedResources[j].Key })
		for _, cap := range o.ReadCapabilities {
			if cap.Target == s.Key {
				r.ReadIDs = append(r.ReadIDs, cap.ID)
			}
		}
		for _, cap := range o.ActionCapabilities {
			if cap.Target == s.Key {
				r.ActionIDs = append(r.ActionIDs, cap.ID)
			}
		}
		if s.Role == "standby" {
			r.Boundary = "planned_standby"
		}
		rows = append(rows, r)
	}
	for _, d := range o.Deployments {
		r := opsOperationRow{ID: "Deployment/" + d.ID, Kind: "deployment", Object: d.Application, Host: d.Host, Deployment: d.ID, Controllers: controllers[d.ID], ReadIDs: []string{}, ActionIDs: []string{}, Applications: []string{d.Application}, SharedWith: []string{}, SharedControllersWith: []string{}, Notice: d.Notice, Boundary: "service_control_not_bound", Prerequisites: []string{"exact_component_identity", "restricted_execution_identity", "readiness_and_traffic_evidence", "reviewed_preflight_and_rollback"}}
		for _, a := range o.Applications {
			if a.Key == d.Application && a.Notice != "" {
				r.Notice = a.Notice
			}
		}
		for _, s := range o.Servers {
			if s.Key == d.Host && s.Notice != "" {
				if r.Notice != "" {
					r.Notice += "；"
				}
				r.Notice += s.Notice
			}
		}
		for _, cap := range o.ReadCapabilities {
			if cap.Target == d.Host && opsCapabilityOwns(o, cap, d.Application) {
				r.ReadIDs = append(r.ReadIDs, cap.ID)
			}
		}
		for _, cap := range o.ActionCapabilities {
			if cap.Target == d.Host && opsCapabilityOwns(o, cap, d.Application) {
				r.ActionIDs = append(r.ActionIDs, cap.ID)
			}
		}
		if len(r.Controllers) == 0 {
			r.Boundary = "controller_unmapped"
		}
		scheduled, stateful := false, false
		for _, c := range d.Criteria {
			scheduled = scheduled || c.WorkloadMode == "scheduled"
			stateful = stateful || c.DataMode == "stateful"
		}
		if scheduled {
			r.Boundary = "scheduled_control_not_bound"
			r.Prerequisites = append(r.Prerequisites, "schedule_mutex_and_cost_limits")
		}
		if stateful {
			r.Boundary = "stateful_control_not_bound"
			r.Prerequisites = append(r.Prerequisites, "roles_replication_and_restore_evidence")
		}
		if len(r.ActionIDs) > 0 {
			r.Boundary = "additional_control_not_bound"
		}
		shared := map[string]bool{}
		coupled := map[string]bool{}
		for _, c := range r.Controllers {
			if c.Kind == "process" {
				continue
			}
			for app := range owners[controllerKey{d.Host, c.Kind, c.Name}] {
				if app != d.Application {
					coupled[app] = true
				}
			}
		}
		for app := range coupled {
			r.SharedControllersWith = append(r.SharedControllersWith, app)
		}
		sort.Strings(r.SharedControllersWith)
		for _, other := range o.Deployments {
			if other.Application == d.Application {
				continue
			}
			for _, resource := range d.Resources {
				if containsOpsKey(other.Resources, resource) {
					shared[other.Application] = true
				}
			}
		}
		for app := range shared {
			r.SharedWith = append(r.SharedWith, app)
		}
		sort.Strings(r.SharedWith)
		rows = append(rows, r)
	}
	return rows
}
