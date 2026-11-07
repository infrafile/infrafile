package main

import (
	"fmt"
	"regexp"
	"time"
)

var opsProductionDigest = regexp.MustCompile(`^[a-f0-9]{64}$`)

func validateOpsProduction(o *opsOverview, now time.Time) error {
	ids := map[string]bool{}
	for i := range o.Applications {
		a := &o.Applications[i]
		p := a.Production
		if p == nil {
			continue
		}
		if len(p.Gates) != 5 || len(p.Boundary) > 2000 {
			return fmt.Errorf("invalid production assessment")
		}
		scope := map[string]bool{}
		for _, d := range o.Deployments {
			if d.Application == a.Key {
				scope[d.Host] = true
			}
		}
		axes := map[string]bool{}
		for j := range p.Gates {
			g := &p.Gates[j]
			switch g.ID {
			case "readiness", "traffic", "replicas", "recovery", "release":
			default:
				return fmt.Errorf("invalid production axis")
			}
			if axes[g.ID] || len(g.Requirements) < 1 || len(g.Requirements) > 8 || len(g.Observations) > 16 {
				return fmt.Errorf("invalid production gate")
			}
			axes[g.ID] = true
			for _, r := range g.Requirements {
				if len(r) == 0 || len(r) > 2000 {
					return fmt.Errorf("invalid production requirement")
				}
			}
			for k := range g.Observations {
				v := &g.Observations[k]
				if !opsCriterionID.MatchString(v.ID) || ids[v.ID] || len(v.Summary) == 0 || len(v.Summary) > 2000 || len(v.Reference) == 0 || len(v.Reference) > 2000 || !opsProductionDigest.MatchString(v.ReferenceSHA256) || len(v.Hosts) < 1 || len(v.Hosts) > 32 {
					return fmt.Errorf("invalid production observation")
				}
				ids[v.ID] = true
				hosts := map[string]bool{}
				for _, h := range v.Hosts {
					if !scope[h] || hosts[h] {
						return fmt.Errorf("production observation scope differs")
					}
					hosts[h] = true
				}
				switch v.Evidence.Status {
				case "PASS", "WARN", "FAIL", "UNKNOWN":
				default:
					return fmt.Errorf("invalid production observation state")
				}
				if v.Evidence.Source != "reviewed-observation" || v.Evidence.ObservedAt == nil || v.Evidence.Reason != "partial_observation_not_admission" {
					return fmt.Errorf("invalid production observation origin")
				}
				if err := opsAge(&v.Evidence, now); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
