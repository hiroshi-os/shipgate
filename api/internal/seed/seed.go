package seed

import (
	"log"
	"strings"

	"shipgate/internal/store"
)

func Run(st *store.Store, demoAppURL, rollbackURL string) error {
	n, err := st.ProjectCount()
	if err != nil {
		return err
	}
	if n > 0 {
		log.Printf("seed: skipped (%d project(s) already present)", n)
		return nil
	}
	demoAppURL = strings.TrimRight(demoAppURL, "/")
	if demoAppURL == "" {
		demoAppURL = "http://demo-app:8080"
	}
	if rollbackURL == "" {
		rollbackURL = "http://hooks:8090/hooks/rollback"
	}
	now := store.Now()

	checkout := store.Project{
		ID:                 "prj_checkout",
		Name:               "checkout-api",
		Slug:               "checkout-api",
		Description:        "Card capture + settlement. Prod is four-eyes gated.",
		RequireApproval:    true,
		RollbackWebhookURL: rollbackURL,
		CreatedAt:          now,
		UpdatedAt:          now,
	}
	checkoutEnvs := []store.Environment{
		{
			ID: "env_checkout_dev", ProjectID: checkout.ID, Name: "Development", Slug: "dev",
			CurrentVersion: "v1.5.0-rc.1", PreviousVersion: "v1.4.2", SortOrder: 0,
			Probes: []store.Probe{{
				ID: "prb_checkout_dev_live", EnvironmentID: "env_checkout_dev",
				Name: "live", URL: demoAppURL + "/health/live", Method: "GET", ExpectedStatus: 200, TimeoutMS: 2000,
			}},
		},
		{
			ID: "env_checkout_staging", ProjectID: checkout.ID, Name: "Staging", Slug: "staging",
			CurrentVersion: "v1.4.2", PreviousVersion: "v1.4.1", SortOrder: 1,
			Probes: []store.Probe{
				{ID: "prb_checkout_stg_live", EnvironmentID: "env_checkout_staging", Name: "live", URL: demoAppURL + "/health/live", Method: "GET", ExpectedStatus: 200, TimeoutMS: 2000},
				{ID: "prb_checkout_stg_ready", EnvironmentID: "env_checkout_staging", Name: "ready", URL: demoAppURL + "/health/ready", Method: "GET", ExpectedStatus: 200, TimeoutMS: 2500},
				{ID: "prb_checkout_stg_pay", EnvironmentID: "env_checkout_staging", Name: "payments", URL: demoAppURL + "/health/payments", Method: "GET", ExpectedStatus: 200, TimeoutMS: 2500},
			},
		},
		{
			ID: "env_checkout_prod", ProjectID: checkout.ID, Name: "Production", Slug: "prod",
			CurrentVersion: "v1.4.1", PreviousVersion: "v1.4.0", SortOrder: 2,
			Probes: []store.Probe{
				{ID: "prb_checkout_prd_live", EnvironmentID: "env_checkout_prod", Name: "live", URL: demoAppURL + "/health/live", Method: "GET", ExpectedStatus: 200, TimeoutMS: 2000},
				{ID: "prb_checkout_prd_ready", EnvironmentID: "env_checkout_prod", Name: "ready", URL: demoAppURL + "/health/ready", Method: "GET", ExpectedStatus: 200, TimeoutMS: 2500},
			},
		},
	}
	if err := st.InsertProject(checkout, checkoutEnvs); err != nil {
		return err
	}
	_ = st.AppendAudit(store.AuditEvent{
		ID: "aud_seed_checkout", ProjectID: checkout.ID, Actor: "shipgate-seed",
		Action: "project.created", Result: "ok", Detail: `{"seed":true}`, CreatedAt: now,
	})

	docs := store.Project{
		ID:                 "prj_docs",
		Name:               "docs-site",
		Slug:               "docs-site",
		Description:        "Public docs. Approval off — health gate still required.",
		RequireApproval:    false,
		RollbackWebhookURL: rollbackURL,
		CreatedAt:          now,
		UpdatedAt:          now,
	}
	docsEnvs := []store.Environment{
		{
			ID: "env_docs_dev", ProjectID: docs.ID, Name: "Development", Slug: "dev",
			CurrentVersion: "v3.2.1-dev", SortOrder: 0,
			Probes: []store.Probe{{
				ID: "prb_docs_dev_live", EnvironmentID: "env_docs_dev",
				Name: "live", URL: demoAppURL + "/health/live", Method: "GET", ExpectedStatus: 200, TimeoutMS: 2000,
			}},
		},
		{
			ID: "env_docs_staging", ProjectID: docs.ID, Name: "Staging", Slug: "staging",
			CurrentVersion: "v3.2.0", PreviousVersion: "v3.1.8", SortOrder: 1,
			Probes: []store.Probe{
				{ID: "prb_docs_stg_live", EnvironmentID: "env_docs_staging", Name: "live", URL: demoAppURL + "/health/live", Method: "GET", ExpectedStatus: 200, TimeoutMS: 2000},
				{ID: "prb_docs_stg_ready", EnvironmentID: "env_docs_staging", Name: "ready", URL: demoAppURL + "/health/ready", Method: "GET", ExpectedStatus: 200, TimeoutMS: 2500},
			},
		},
		{
			ID: "env_docs_prod", ProjectID: docs.ID, Name: "Production", Slug: "prod",
			CurrentVersion: "v3.1.8", PreviousVersion: "v3.1.7", SortOrder: 2,
			Probes: []store.Probe{{
				ID: "prb_docs_prd_live", EnvironmentID: "env_docs_prod",
				Name: "live", URL: demoAppURL + "/health/live", Method: "GET", ExpectedStatus: 200, TimeoutMS: 2000,
			}},
		},
	}
	if err := st.InsertProject(docs, docsEnvs); err != nil {
		return err
	}
	_ = st.AppendAudit(store.AuditEvent{
		ID: "aud_seed_docs", ProjectID: docs.ID, Actor: "shipgate-seed",
		Action: "project.created", Result: "ok", Detail: `{"seed":true}`, CreatedAt: now,
	})
	log.Printf("seed: checkout-api (approval on) + docs-site (approval off); probes → %s; rollback → %s", demoAppURL, rollbackURL)
	return nil
}
