package main

import (
	"fmt"
	"strings"
)

type deliveryStage struct {
	Name  string
	Kind  string
	Focus string
}

type projectGraph struct {
	Plan, Draft, Audit, SecondAudit, Verify string
	PlanFocus, SecondAuditFocus             string
}

var projectGraphs = map[string]projectGraph{
	"general": {
		"scope_deliverable", "assemble_deliverable", "audit_completeness", "audit_consistency", "check_handoff",
		"Identify the requested format, intended reader, acceptance criteria, missing inputs, and what can be verified.",
		"Cross-check internal references, names, quantities, units, and deliverable usability.",
	},
	"software": {
		"trace_entrypoints", "implement_change", "audit_contracts", "audit_failure_paths", "verify_regressions",
		"Trace normal entry points, callers, persistence, and existing tests. List compatibility guarantees and prioritized regression scenarios.",
		"Inspect empty, stale, concurrent, retry, cancellation, and partial-failure paths where relevant. Check state invariants and rollback behavior.",
	},
	"software-new": {
		"define_first_milestone", "build_runnable_slice", "audit_entrypoints", "audit_configuration", "verify_clean_checkout",
		"Define the first end-to-end scenario, runtime constraints, dependencies, acceptance command, and minimum project structure.",
		"Audit dependency declarations, example settings, fixture data, runtime prerequisites, and handling of missing services or credentials.",
	},
	"research": {
		"design_evidence_protocol", "synthesize_findings", "audit_claim_sources", "audit_methods_and_uncertainty", "verify_reproducibility",
		"Define the question, time window, inclusion criteria, primary-source preference, and gaps in the supplied evidence. Give a reproducibility checklist.",
		"Compare methods, dates, populations, units and denominators. Recompute important quantities and identify material contradictory evidence or uncertainty.",
	},
	"pitch": {
		"map_buyer_decision", "assemble_pitch", "audit_claims_and_metrics", "audit_message_and_ask", "verify_recipient_handoff",
		"Identify the audience, buying or funding decision, format limits, objections, credible proof, and the requested next action.",
		"Read as a skeptical recipient. Resolve inconsistencies between the narrative and numbers, and check that the ask and next action are explicit.",
	},
	"grants": {
		"map_solicitation_requirements", "draft_grant_package", "audit_eligibility_and_rubric", "audit_budget_and_milestones", "verify_submission_readiness",
		"Extract the funder's eligibility, evaluation rubric, limits, deadline and time zone, required attachments, and budget rules. Map each to evidence or an open question.",
		"Reconcile totals, line items, match commitments, indirect costs, milestones, and measurable outcomes. Flag unsupported commitments and uncertain eligibility.",
	},
	"job-search": {
		"define_role_filters", "tailor_application_materials", "audit_role_eligibility", "audit_experience_claims", "verify_application_package",
		"List hard filters for role, location, remote status, pay, authorization, and excluded industries. Map requirements to verifiable experience and record missing facts.",
		"Audit titles, dates, skills, claimed outcomes, and fit against verified experience. Distinguish current posting facts from stale or unknown information.",
	},
}

// Evidence-heavy projects capture source constraints before synthesis or tailoring.
var evidenceStages = map[string]deliveryStage{
	"research":   {"inventory_research_sources", "evidence", "List supplied sources, dates, primary or secondary status, datasets, and what each can substantiate. Flag gaps, stale material, and any claims requiring additional evidence."},
	"grants":     {"extract_solicitation_rules", "evidence", "Extract funder rules from the supplied solicitation. Record source section, exact threshold or limit, timing and time zone, and required evidence. Mark ambiguous or missing requirements."},
	"job-search": {"inventory_role_evidence", "evidence", "Separate posting facts, verified candidate experience, and unconfirmed information. Identify missing dates, locations, pay details, and requirements that need evidence."},
}

func deliveryStages(name string, profile AgentProfile) []deliveryStage {
	domain, specialized := projectGraphs[strings.TrimPrefix(name, "project:")]
	if !strings.HasPrefix(name, "project:") || !specialized {
		domain = projectGraph{Plan: "plan", Draft: "draft", Audit: "review", SecondAudit: "review", Verify: "verify"}
	}
	secondFocus := profile.SecondAuditFocus
	if strings.TrimSpace(secondFocus) == "" {
		secondFocus = domain.SecondAuditFocus
	}
	var stages []deliveryStage
	if strings.TrimSpace(profile.PlanFocus) != "" {
		if evidence, ok := evidenceStages[strings.TrimPrefix(name, "project:")]; specialized && ok && strings.HasPrefix(name, "project:") {
			stages = append(stages, evidence)
		}
		stages = append(stages, deliveryStage{domain.Plan, "plan", profile.PlanFocus})
	}
	stages = append(stages, deliveryStage{domain.Draft, "draft", profile.DraftFocus})
	for i := 0; i < profile.ReviewPasses; i++ {
		stage := deliveryStage{domain.Audit, "review", profile.ReviewFocus}
		if i > 0 {
			stage.Name = domain.SecondAudit
			stage.Focus = strings.TrimSpace(profile.ReviewFocus + "\n" + secondFocus)
			if stage.Name == domain.Audit || i > 1 {
				stage.Name = fmt.Sprintf("%s_%d", stage.Name, i+1)
			}
		}
		if i == profile.ReviewPasses-1 && i > 0 && profile.VerificationFocus != "" {
			stage = deliveryStage{domain.Verify, "verify", profile.VerificationFocus}
		}
		stages = append(stages, stage)
	}
	return append(stages, deliveryStage{Name: "validate_delivery", Kind: "validate"})
}
