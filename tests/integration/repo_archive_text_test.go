// Copyright 2024 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package integration

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"forgejo.org/modules/translation"
	"forgejo.org/tests/forgery"

	"github.com/PuerkitoBio/goquery"
	"github.com/stretchr/testify/assert"
)

func TestArchiveText(t *testing.T) {
	onApplicationRun(t, func(t *testing.T, giteaURL *url.URL) {
		tr := translation.NewLocale("en-US")

		repo := forgery.CreateRepository(t, nil, nil)
		session := loginUser(t, repo.Owner.Name)
		link := repo.HTMLURL() + "/settings"

		// Test settings page
		req := NewRequest(t, "GET", link)
		resp := session.MakeRequest(t, req, http.StatusOK)
		archivation := NewHTMLParser(t, resp.Body)
		testRepoArchiveElements(t, tr, archivation, "archive")

		// Archive repo
		req = NewRequestWithValues(t, "POST", link, map[string]string{
			"action": "archive",
		})
		_ = session.MakeRequest(t, req, http.StatusSeeOther)

		// Test settings page again
		req = NewRequest(t, "GET", link)
		resp = session.MakeRequest(t, req, http.StatusOK)
		unarchivation := NewHTMLParser(t, resp.Body)
		testRepoArchiveElements(t, tr, unarchivation, "unarchive")
	})
}

func testRepoArchiveElements(t *testing.T, tr translation.Locale, doc *HTMLDoc, opType string) {
	t.Helper()

	// Test danger section
	section := doc.Find(".danger.segment .flex-list .flex-item:has(.button[data-modal='#archive-repo-modal'])")
	testRepoArchiveElement(t, tr, section, ".flex-item-title", opType+".header")
	testRepoArchiveElement(t, tr, section, ".flex-item-body", opType+".text")
	testRepoArchiveElement(t, tr, section, ".button", opType+".button")

	// Test modal
	modal := doc.Find("#archive-repo-modal")
	testRepoArchiveElement(t, tr, modal, ".header", opType+".header")
	testRepoArchiveElement(t, tr, modal, ".message", opType+".text")
	testRepoArchiveElement(t, tr, modal, ".button.red", opType+".button")
}

func testRepoArchiveElement(t *testing.T, tr translation.Locale, doc *goquery.Selection, selector, op string) {
	t.Helper()

	element := doc.Find(selector).Text()
	element = strings.TrimSpace(element)
	assert.Equal(t, tr.TrString("repo.settings."+op), element)
}
