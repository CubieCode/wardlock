// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package main

// The demo owner's sites, groups, tags and links. Every name, label and
// address is invented; addresses use the reserved .test domain.

type tagPlan struct {
	name   string
	colour int
}

type groupPlan struct {
	name, note string
}

// policyPlan is one `wardlock policy set` call: path "" is the site
// default. password is piped to the CLI on standard input.
type policyPlan struct {
	path     string
	public   bool
	links    bool
	password string
	allow    string // with email: the allowlist
}

// linkPlan is one share link. ageDays backdates its creation; links that
// expire within a week keep the time they were really created (see
// backdatedLink).
type linkPlan struct {
	label   string
	expires string // the CLI's --expires
	path    string
	ageDays float64
}

type sitePlan struct {
	name    string
	fixture string // directory under fixtures/sites
	group   string
	tags    []string
	pinned  bool
	ageDays float64 // when the site was created
	// redeployAgeDays, when set, adds a second deploy that many days ago.
	redeployAgeDays float64
	policies        []policyPlan
	links           []linkPlan
	traffic         trafficPlan
}

var tags = []tagPlan{
	{"draft", 1}, {"live", 2}, {"review", 3}, {"archived", 4},
	{"client-tui", 5}, {"client-harbour", 6}, {"docs", 7}, {"mockup", 8},
}

var groups = []groupPlan{
	{"Client work", "Sites we host for clients"},
	{"Marketing", "Launches, campaigns and landing pages"},
	{"Internal", "Team-only docs and tools"},
	{"Experiments", ""},
}

// demoPassword is the password of every password-protected demo site.
const demoPassword = "demo-password-1234"

var (
	public       = []policyPlan{{public: true}}
	linksOnly    = []policyPlan{{links: true}}
	withPassword = []policyPlan{{links: true, password: demoPassword}}
)

var sites = []sitePlan{
	{
		name: "tui-brand-refresh", fixture: "basic", group: "Client work", tags: []string{"client-tui", "review"},
		pinned: true, ageDays: 74, redeployAgeDays: 9, policies: linksOnly,
		links: []linkPlan{
			{label: "Client review, round 2", expires: "never", ageDays: 60},
			{label: "Tui marketing team", expires: "30d", ageDays: 12},
			{label: "Quick look", expires: "30m", ageDays: 20},
		},
		traffic: trafficPlan{daily: 100, shape: shapeSteady, work: true},
	},
	{
		name: "tui-launch-preview", fixture: "spa", group: "Client work", tags: []string{"client-tui", "draft"},
		ageDays: 21, policies: withPassword,
		links:   []linkPlan{{label: "Stakeholder preview", expires: "1d", ageDays: 5}},
		traffic: trafficPlan{daily: 60, shape: shapeRising, work: true},
	},
	{
		name: "harbour-annual-report", fixture: "renditions", group: "Client work", tags: []string{"client-harbour", "live"},
		ageDays: 63, policies: []policyPlan{{allow: "harbour.example.test,board@example.test"}},
		traffic: trafficPlan{daily: 80, shape: shapeSteady, work: true},
	},
	{
		name: "harbour-booking-mockup", fixture: "spa", group: "Client work", tags: []string{"client-harbour", "mockup"},
		ageDays: 34, policies: linksOnly,
		links: []linkPlan{
			{label: "Harbour UX review", expires: "14d", ageDays: 6},
			{label: "Usability session", expires: "36h", ageDays: 3},
		},
		traffic: trafficPlan{daily: 65, shape: shapeSteady, work: true},
	},
	{
		name: "harbour-wayfinding", fixture: "basic", group: "Client work", tags: []string{"client-harbour", "archived"},
		ageDays: 88, policies: withPassword,
		traffic: trafficPlan{daily: 70, shape: shapeDead, deadAgeDays: 41, work: true},
	},
	{
		name: "spring-launch", fixture: "spa", group: "Marketing", tags: []string{"live"},
		pinned: true, ageDays: 30, redeployAgeDays: 24, policies: public,
		traffic: trafficPlan{daily: 200, shape: shapeLaunch, launchAgeDays: 24},
	},
	{
		name: "pricing-page", fixture: "basic", group: "Marketing", tags: []string{"live"},
		ageDays: 89, redeployAgeDays: 16, policies: public,
		traffic: trafficPlan{daily: 230, shape: shapeSteady},
	},
	{
		name: "newsletter-archive", fixture: "renditions", group: "Marketing", tags: []string{"live"},
		ageDays: 80, policies: public,
		traffic: trafficPlan{daily: 100, shape: shapeRising},
	},
	{
		name: "winter-sale", fixture: "basic", group: "Marketing", tags: []string{"archived"},
		ageDays: 87, policies: public,
		traffic: trafficPlan{daily: 220, shape: shapeFading},
	},
	{
		name: "team-handbook", fixture: "renditions", group: "Internal", tags: []string{"docs", "live"},
		pinned: true, ageDays: 86, redeployAgeDays: 4, policies: []policyPlan{{allow: "example.test"}},
		traffic: trafficPlan{daily: 90, shape: shapeSteady, work: true, local: 0.8},
	},
	{
		name: "api-docs", fixture: "renditions", group: "Internal", tags: []string{"docs"},
		ageDays: 70, policies: []policyPlan{{public: true}, {path: "/docs/", password: demoPassword}},
		traffic: trafficPlan{daily: 70, shape: shapeSteady, work: true},
	},
	{
		name: "design-system", fixture: "basic", group: "Internal", tags: []string{"docs", "review"},
		ageDays: 55, policies: withPassword,
		links:   []linkPlan{{label: "Contractor access", expires: "30d", ageDays: 10}},
		traffic: trafficPlan{daily: 50, shape: shapeSteady, work: true, local: 0.7},
	},
	{
		name: "status-board", fixture: "spa", group: "Internal", tags: []string{"live"},
		pinned: true, ageDays: 82, policies: linksOnly,
		links: []linkPlan{
			{label: "Office TV", expires: "never", ageDays: 80},
			{label: "On-call", expires: "6h", ageDays: 1},
		},
		traffic: trafficPlan{daily: 70, shape: shapeSteady, local: 0.9},
	},
	{
		name: "map-prototype", fixture: "spa", group: "Experiments", tags: []string{"draft", "mockup"},
		ageDays: 26, redeployAgeDays: 2, policies: linksOnly,
		links: []linkPlan{
			{label: "Prototype feedback", expires: "never", ageDays: 25},
			{label: "Investor demo", expires: "2w", ageDays: 4},
		},
		traffic: trafficPlan{daily: 45, shape: shapeRising},
	},
	{
		name: "ai-readme", fixture: "renditions", group: "Experiments", tags: []string{"draft"},
		ageDays: 47, policies: public,
		traffic: trafficPlan{daily: 60, shape: shapeFading, tools: true},
	},
	{
		name: "offline-test", fixture: "basic", group: "Experiments",
		ageDays: 12, policies: public,
		traffic: trafficPlan{daily: 8, shape: shapeSteady},
	},
	{
		name: "holiday-photos", fixture: "basic", policies: withPassword, ageDays: 40,
		traffic: trafficPlan{daily: 15, shape: shapeFading, local: 0.6},
	},
	{
		name: "scratch", fixture: "spa", tags: []string{"draft"}, policies: public, ageDays: 15,
		traffic: trafficPlan{daily: 6, shape: shapeSteady, tools: true},
	},
}

// fixturePaths are the pages and assets each fixture serves.
var fixturePaths = map[string][]string{
	"basic":      {"/", "/guide", "/about/", "/assets/site.css", "/assets/app.js"},
	"spa":        {"/", "/assets/app.js", "/bookings", "/settings"},
	"renditions": {"/", "/docs/setup.html", "/docs/untitled.htm", "/docs/data.json"},
}
