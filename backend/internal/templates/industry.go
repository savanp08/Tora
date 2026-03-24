package templates

import "strings"

type IndustryTemplate struct {
	ID              string          `json:"id"`
	Name            string          `json:"name"`
	Description     string          `json:"description"`
	Industries      []string        `json:"industries"`
	FieldSchemas    []TemplateField `json:"field_schemas"`
	SampleTasks     []TemplateTask  `json:"sample_tasks,omitempty"`
	AutomationRules []TemplateRule  `json:"automation_rules,omitempty"`
}

type TemplateField struct {
	Name      string   `json:"name"`
	FieldType string   `json:"field_type"`
	Options   []string `json:"options,omitempty"`
	Position  int      `json:"position"`
}

type TemplateTask struct {
	Title        string            `json:"title"`
	Status       string            `json:"status"`
	SprintName   string            `json:"sprint_name,omitempty"`
	CustomFields map[string]string `json:"custom_fields,omitempty"`
}

type TemplateRule struct {
	Name          string `json:"name"`
	TriggerType   string `json:"trigger_type"`
	TriggerConfig string `json:"trigger_config"`
	ActionType    string `json:"action_type"`
	ActionConfig  string `json:"action_config"`
}

var builtInIndustryTemplates = []IndustryTemplate{
	{
		ID:          "software-sprint",
		Name:        "Software Team Sprint",
		Description: "Launch a product sprint with engineering-friendly starter fields, priorities, and a first delivery backlog.",
		Industries:  []string{"Software", "Product", "Engineering"},
		FieldSchemas: []TemplateField{
			{Name: "Story Points", FieldType: "number", Position: 0},
			{Name: "Type", FieldType: "select", Options: []string{"Feature", "Bug", "Chore", "Spike"}, Position: 1},
			{Name: "Priority", FieldType: "select", Options: []string{"P0", "P1", "P2", "P3"}, Position: 2},
			{Name: "PR Link", FieldType: "url", Position: 3},
			{Name: "Epic", FieldType: "text", Position: 4},
		},
		SampleTasks: []TemplateTask{
			{Title: "Set up project repo", Status: "todo", SprintName: "Sprint 1", CustomFields: map[string]string{"Story Points": "3", "Type": "Chore", "Priority": "P1", "Epic": "Platform"}},
			{Title: "Design system architecture", Status: "todo", SprintName: "Sprint 1", CustomFields: map[string]string{"Story Points": "5", "Type": "Spike", "Priority": "P1", "Epic": "Foundation"}},
			{Title: "Implement auth", Status: "todo", SprintName: "Sprint 1", CustomFields: map[string]string{"Story Points": "8", "Type": "Feature", "Priority": "P0", "Epic": "Identity"}},
			{Title: "Write unit tests", Status: "todo", SprintName: "Sprint 1", CustomFields: map[string]string{"Story Points": "5", "Type": "Chore", "Priority": "P1", "Epic": "Quality"}},
			{Title: "Deploy to staging", Status: "todo", SprintName: "Sprint 1", CustomFields: map[string]string{"Story Points": "3", "Type": "Chore", "Priority": "P1", "Epic": "Release"}},
		},
		AutomationRules: []TemplateRule{
			{
				Name:          "Escalate critical bugs",
				TriggerType:   "field_changes",
				TriggerConfig: `{"conditions":[{"field_name":"Type","equals":"Bug"},{"field_name":"Priority","equals":"P0"}]}`,
				ActionType:    "set_status",
				ActionConfig:  `{"status":"in_progress"}`,
			},
		},
	},
	{
		ID:          "marketing-campaign",
		Name:        "Marketing Campaign",
		Description: "Organize campaign planning, asset production, launch coordination, and reporting in one board.",
		Industries:  []string{"Marketing", "Growth", "Content"},
		FieldSchemas: []TemplateField{
			{Name: "Channel", FieldType: "select", Options: []string{"Social", "Email", "Blog", "Paid", "SEO"}, Position: 0},
			{Name: "Publish Date", FieldType: "date", Position: 1},
			{Name: "Campaign", FieldType: "text", Position: 2},
			{Name: "Target Audience", FieldType: "text", Position: 3},
			{Name: "Status Notes", FieldType: "text", Position: 4},
		},
		SampleTasks: []TemplateTask{
			{Title: "Define campaign goals", Status: "todo", SprintName: "Campaign Planning", CustomFields: map[string]string{"Campaign": "Spring Launch", "Target Audience": "Prospects"}},
			{Title: "Create content calendar", Status: "todo", SprintName: "Campaign Planning", CustomFields: map[string]string{"Campaign": "Spring Launch", "Channel": "Blog"}},
			{Title: "Draft social copy", Status: "todo", SprintName: "Asset Production", CustomFields: map[string]string{"Campaign": "Spring Launch", "Channel": "Social"}},
			{Title: "Design assets", Status: "todo", SprintName: "Asset Production", CustomFields: map[string]string{"Campaign": "Spring Launch", "Channel": "Paid"}},
			{Title: "Schedule posts", Status: "todo", SprintName: "Launch Week", CustomFields: map[string]string{"Campaign": "Spring Launch", "Channel": "Social"}},
			{Title: "Report results", Status: "todo", SprintName: "Launch Week", CustomFields: map[string]string{"Campaign": "Spring Launch", "Channel": "Email"}},
		},
		AutomationRules: []TemplateRule{
			{
				Name:          "Notify assignee on completion",
				TriggerType:   "status_changes",
				TriggerConfig: `{"to":"done"}`,
				ActionType:    "notify_member",
				ActionConfig:  `{"target":"assignee","message":"Campaign task marked done. Time to review impact."}`,
			},
		},
	},
	{
		ID:          "construction-project",
		Name:        "Construction / Civil Project",
		Description: "Track site work, permits, inspections, and handover across planning and execution phases.",
		Industries:  []string{"Construction", "Civil", "Operations"},
		FieldSchemas: []TemplateField{
			{Name: "Phase", FieldType: "select", Options: []string{"Planning", "Permits", "Foundation", "Structure", "Finishing", "Handover"}, Position: 0},
			{Name: "Site Location", FieldType: "text", Position: 1},
			{Name: "Contractor", FieldType: "text", Position: 2},
			{Name: "Permit Number", FieldType: "text", Position: 3},
			{Name: "Inspection Date", FieldType: "date", Position: 4},
			{Name: "Budget Code", FieldType: "text", Position: 5},
		},
		SampleTasks: []TemplateTask{
			{Title: "Site survey", Status: "todo", SprintName: "Phase 1 - Planning", CustomFields: map[string]string{"Phase": "Planning", "Site Location": "Primary site", "Budget Code": "PLAN-001"}},
			{Title: "Permit application", Status: "todo", SprintName: "Phase 1 - Planning", CustomFields: map[string]string{"Phase": "Permits", "Permit Number": "TBD", "Budget Code": "PERM-002"}},
			{Title: "Foundation pour", Status: "todo", SprintName: "Phase 2 - Construction", CustomFields: map[string]string{"Phase": "Foundation", "Contractor": "General Contractor", "Budget Code": "CONS-101"}},
			{Title: "Framing inspection", Status: "todo", SprintName: "Phase 2 - Construction", CustomFields: map[string]string{"Phase": "Structure", "Inspection Date": "2026-04-15", "Budget Code": "CONS-111"}},
			{Title: "Electrical rough-in", Status: "todo", SprintName: "Phase 2 - Construction", CustomFields: map[string]string{"Phase": "Structure", "Contractor": "Electrical Team", "Budget Code": "CONS-118"}},
			{Title: "Final walkthrough", Status: "todo", SprintName: "Phase 2 - Construction", CustomFields: map[string]string{"Phase": "Handover", "Budget Code": "HAND-201"}},
		},
	},
	{
		ID:          "editorial-workflow",
		Name:        "Journalism / Editorial",
		Description: "Run a newsroom workflow from pitch through review, fact checking, and publication.",
		Industries:  []string{"Editorial", "Publishing", "Journalism"},
		FieldSchemas: []TemplateField{
			{Name: "Publication", FieldType: "text", Position: 0},
			{Name: "Section", FieldType: "select", Options: []string{"News", "Feature", "Opinion", "Review"}, Position: 1},
			{Name: "Due Date", FieldType: "date", Position: 2},
			{Name: "Word Count", FieldType: "number", Position: 3},
			{Name: "Editor", FieldType: "person", Position: 4},
			{Name: "Source Count", FieldType: "number", Position: 5},
		},
		SampleTasks: []TemplateTask{
			{Title: "Story pitch - TBD", Status: "todo", SprintName: "Editorial Queue", CustomFields: map[string]string{"Section": "Feature", "Editor": "Managing Editor"}},
			{Title: "Research sources", Status: "todo", SprintName: "Editorial Queue", CustomFields: map[string]string{"Source Count": "5", "Section": "News"}},
			{Title: "First draft", Status: "todo", SprintName: "Editorial Queue", CustomFields: map[string]string{"Word Count": "1200", "Section": "Feature"}},
			{Title: "Editor review", Status: "todo", SprintName: "Editorial Queue", CustomFields: map[string]string{"Editor": "Managing Editor", "Section": "Feature"}},
			{Title: "Fact check", Status: "todo", SprintName: "Editorial Queue", CustomFields: map[string]string{"Source Count": "7", "Section": "News"}},
			{Title: "Final edit", Status: "todo", SprintName: "Editorial Queue", CustomFields: map[string]string{"Editor": "Managing Editor", "Section": "Feature"}},
			{Title: "Publish", Status: "todo", SprintName: "Editorial Queue", CustomFields: map[string]string{"Publication": "Weekly Desk", "Section": "Feature"}},
		},
		AutomationRules: []TemplateRule{
			{
				Name:          "Notify editor on completion",
				TriggerType:   "status_changes",
				TriggerConfig: `{"to":"done"}`,
				ActionType:    "notify_member",
				ActionConfig:  `{"target_field":"Editor","message":"Story task completed and ready for editorial review."}`,
			},
		},
	},
	{
		ID:          "service-delivery",
		Name:        "Service Business Delivery",
		Description: "Support agencies, consulting teams, and IT service operations with SLA and client tracking.",
		Industries:  []string{"Agency", "Consulting", "IT Services"},
		FieldSchemas: []TemplateField{
			{Name: "Client", FieldType: "text", Position: 0},
			{Name: "Priority", FieldType: "select", Options: []string{"Critical", "High", "Medium", "Low"}, Position: 1},
			{Name: "SLA Deadline", FieldType: "date", Position: 2},
			{Name: "Ticket Number", FieldType: "text", Position: 3},
			{Name: "Resolution Notes", FieldType: "text", Position: 4},
		},
		SampleTasks: []TemplateTask{
			{Title: "Client onboarding", Status: "todo", SprintName: "Delivery Pipeline", CustomFields: map[string]string{"Client": "New Account", "Priority": "High"}},
			{Title: "Requirements gathering", Status: "todo", SprintName: "Delivery Pipeline", CustomFields: map[string]string{"Client": "New Account", "Priority": "High"}},
			{Title: "Proposal draft", Status: "todo", SprintName: "Delivery Pipeline", CustomFields: map[string]string{"Client": "New Account", "Priority": "Medium"}},
			{Title: "Delivery", Status: "todo", SprintName: "Delivery Pipeline", CustomFields: map[string]string{"Client": "New Account", "Priority": "Critical"}},
			{Title: "QA review", Status: "todo", SprintName: "Delivery Pipeline", CustomFields: map[string]string{"Client": "New Account", "Priority": "High"}},
			{Title: "Client sign-off", Status: "todo", SprintName: "Delivery Pipeline", CustomFields: map[string]string{"Client": "New Account", "Priority": "Medium"}},
			{Title: "Invoice", Status: "todo", SprintName: "Delivery Pipeline", CustomFields: map[string]string{"Client": "New Account", "Priority": "Low"}},
		},
		AutomationRules: []TemplateRule{
			{
				Name:          "Fast-track critical work",
				TriggerType:   "field_changes",
				TriggerConfig: `{"field_name":"Priority","equals":"Critical"}`,
				ActionType:    "set_status",
				ActionConfig:  `{"status":"in_progress"}`,
			},
		},
	},
	{
		ID:          "general-project",
		Name:        "General Project",
		Description: "A lightweight starting point with just enough structure for broad planning and delivery work.",
		Industries:  []string{"General", "Operations", "Cross-functional"},
		FieldSchemas: []TemplateField{
			{Name: "Priority", FieldType: "select", Options: []string{"High", "Medium", "Low"}, Position: 0},
			{Name: "Due Date", FieldType: "date", Position: 1},
			{Name: "Category", FieldType: "text", Position: 2},
		},
		SampleTasks: []TemplateTask{
			{Title: "Project kickoff", Status: "todo", SprintName: "Backlog", CustomFields: map[string]string{"Priority": "High", "Category": "Planning"}},
			{Title: "Define scope", Status: "todo", SprintName: "Backlog", CustomFields: map[string]string{"Priority": "High", "Category": "Planning"}},
			{Title: "Delivery", Status: "todo", SprintName: "Backlog", CustomFields: map[string]string{"Priority": "Medium", "Category": "Execution"}},
			{Title: "Review", Status: "todo", SprintName: "Backlog", CustomFields: map[string]string{"Priority": "Medium", "Category": "Wrap-up"}},
		},
	},
}

func ListIndustryTemplates(includeTasks bool) []IndustryTemplate {
	templates := make([]IndustryTemplate, 0, len(builtInIndustryTemplates))
	for _, template := range builtInIndustryTemplates {
		templates = append(templates, cloneIndustryTemplate(template, includeTasks))
	}
	return templates
}

func FindIndustryTemplateByID(rawID string) (IndustryTemplate, bool) {
	normalizedID := strings.TrimSpace(strings.ToLower(rawID))
	for _, template := range builtInIndustryTemplates {
		if strings.EqualFold(template.ID, normalizedID) {
			return cloneIndustryTemplate(template, true), true
		}
	}
	return IndustryTemplate{}, false
}

func cloneIndustryTemplate(template IndustryTemplate, includeTasks bool) IndustryTemplate {
	cloned := IndustryTemplate{
		ID:              template.ID,
		Name:            template.Name,
		Description:     template.Description,
		Industries:      append([]string(nil), template.Industries...),
		FieldSchemas:    append([]TemplateField(nil), template.FieldSchemas...),
		AutomationRules: append([]TemplateRule(nil), template.AutomationRules...),
	}
	if includeTasks {
		cloned.SampleTasks = make([]TemplateTask, 0, len(template.SampleTasks))
		for _, task := range template.SampleTasks {
			cloned.SampleTasks = append(cloned.SampleTasks, TemplateTask{
				Title:        task.Title,
				Status:       task.Status,
				SprintName:   task.SprintName,
				CustomFields: cloneTemplateTaskFields(task.CustomFields),
			})
		}
	}
	return cloned
}

func cloneTemplateTaskFields(source map[string]string) map[string]string {
	if len(source) == 0 {
		return nil
	}
	cloned := make(map[string]string, len(source))
	for key, value := range source {
		cloned[key] = value
	}
	return cloned
}
