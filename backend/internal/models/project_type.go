package models

import (
	"strings"
	"time"
)

const DefaultProjectType = "software"

type ProjectTypeConfig struct {
	Type            string
	DisplayName     string
	GroupTerm       string
	GroupTermPlural string
	TaskTerm        string
	TaskTermPlural  string
	MemberTerm      string
	StatusOptions   []string
	DefaultRoles    []string
	Description     string
}

type Group struct {
	WorkspaceID  string    `json:"workspace_id"`
	GroupID      string    `json:"group_id"`
	Name         string    `json:"name"`
	DisplayOrder int       `json:"display_order"`
	StartDate    string    `json:"start_date"`
	EndDate      string    `json:"end_date"`
	Description  string    `json:"description"`
	CreatedAt    time.Time `json:"created_at"`
}

var ProjectTypeConfigs = map[string]ProjectTypeConfig{
	"software": {
		Type:            "software",
		DisplayName:     "Software Development",
		GroupTerm:       "Sprint",
		GroupTermPlural: "Sprints",
		TaskTerm:        "Task",
		TaskTermPlural:  "Tasks",
		MemberTerm:      "Member",
		StatusOptions:   []string{"Backlog", "Todo", "In Progress", "In Review", "Done"},
		DefaultRoles:    []string{"Backend Developer", "Frontend Developer", "DevOps Engineer", "QA Engineer", "UI/UX Designer", "Product Manager"},
		Description:     "Tasks grouped into Sprints",
	},
	"marketing": {
		Type:            "marketing",
		DisplayName:     "Marketing",
		GroupTerm:       "Campaign",
		GroupTermPlural: "Campaigns",
		TaskTerm:        "Deliverable",
		TaskTermPlural:  "Deliverables",
		MemberTerm:      "Member",
		StatusOptions:   []string{"Planned", "In Progress", "In Review", "Published", "Done"},
		DefaultRoles:    []string{"Campaign Manager", "Copywriter", "Designer", "SEO Specialist", "Social Media Manager", "Analyst"},
		Description:     "Deliverables grouped into Campaigns",
	},
	"construction": {
		Type:            "construction",
		DisplayName:     "Construction",
		GroupTerm:       "Phase",
		GroupTermPlural: "Phases",
		TaskTerm:        "Work Package",
		TaskTermPlural:  "Work Packages",
		MemberTerm:      "Crew Member",
		StatusOptions:   []string{"Planned", "Mobilising", "In Progress", "Inspection", "Complete"},
		DefaultRoles:    []string{"Project Manager", "Site Engineer", "Architect", "Safety Officer", "Electrician", "Structural Engineer"},
		Description:     "Work Packages grouped into Phases",
	},
	"journalism": {
		Type:            "journalism",
		DisplayName:     "Journalism",
		GroupTerm:       "Assignment",
		GroupTermPlural: "Assignments",
		TaskTerm:        "Story",
		TaskTermPlural:  "Stories",
		MemberTerm:      "Reporter",
		StatusOptions:   []string{"Pitch", "Researching", "Writing", "Editing", "Published"},
		DefaultRoles:    []string{"Editor", "Reporter", "Photographer", "Fact-checker", "Copy Editor", "Producer"},
		Description:     "Stories grouped into Assignments",
	},
	"electrical": {
		Type:            "electrical",
		DisplayName:     "Electrical Engineering",
		GroupTerm:       "Stage",
		GroupTermPlural: "Stages",
		TaskTerm:        "Work Order",
		TaskTermPlural:  "Work Orders",
		MemberTerm:      "Technician",
		StatusOptions:   []string{"Planned", "Scheduled", "In Progress", "Testing", "Commissioned"},
		DefaultRoles:    []string{"Electrical Engineer", "Technician", "Inspector", "Project Manager", "CAD Designer", "Safety Officer"},
		Description:     "Work Orders grouped into Stages",
	},
	"business": {
		Type:            "business",
		DisplayName:     "Business / Operations",
		GroupTerm:       "Initiative",
		GroupTermPlural: "Initiatives",
		TaskTerm:        "Action Item",
		TaskTermPlural:  "Action Items",
		MemberTerm:      "Member",
		StatusOptions:   []string{"Proposed", "Approved", "In Progress", "On Hold", "Closed"},
		DefaultRoles:    []string{"Operations Manager", "Business Analyst", "Stakeholder", "Finance Lead", "HR Lead", "Executive Sponsor"},
		Description:     "Action Items grouped into Initiatives",
	},
	"general": {
		Type:            "general",
		DisplayName:     "General",
		GroupTerm:       "Phase",
		GroupTermPlural: "Phases",
		TaskTerm:        "Task",
		TaskTermPlural:  "Tasks",
		MemberTerm:      "Member",
		StatusOptions:   []string{"Todo", "In Progress", "Done"},
		DefaultRoles:    []string{"Lead", "Member", "Contributor"},
		Description:     "Tasks grouped into Phases",
	},
}

func GetProjectTypeConfig(projectType string) ProjectTypeConfig {
	if cfg, ok := ProjectTypeConfigs[strings.ToLower(strings.TrimSpace(projectType))]; ok {
		return cfg
	}
	return ProjectTypeConfigs["general"]
}

func NormalizeProjectType(projectType string) string {
	normalized := strings.ToLower(strings.TrimSpace(projectType))
	if normalized == "" {
		return DefaultProjectType
	}
	if _, ok := ProjectTypeConfigs[normalized]; ok {
		return normalized
	}
	return DefaultProjectType
}
