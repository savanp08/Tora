import { derived, writable } from 'svelte/store';

export type ProjectType =
	| 'software'
	| 'marketing'
	| 'construction'
	| 'journalism'
	| 'electrical'
	| 'business'
	| 'general';

export type ProjectTypeConfig = {
	type: ProjectType;
	displayName: string;
	groupTerm: string;
	groupTermPlural: string;
	taskTerm: string;
	taskTermPlural: string;
	memberTerm: string;
	statusOptions: string[];
	defaultRoles: string[];
	description: string;
	icon: string;
};

export type CurrentWorkspace = {
	id: string;
	name?: string;
	project_type?: string;
};

export const PROJECT_TYPE_CONFIGS: Record<ProjectType, ProjectTypeConfig> = {
	software: {
		type: 'software',
		displayName: 'Software Development',
		groupTerm: 'Sprint',
		groupTermPlural: 'Sprints',
		taskTerm: 'Task',
		taskTermPlural: 'Tasks',
		memberTerm: 'Member',
		statusOptions: ['Backlog', 'Todo', 'In Progress', 'In Review', 'Done'],
		defaultRoles: [
			'Backend Developer',
			'Frontend Developer',
			'DevOps Engineer',
			'QA Engineer',
			'UI/UX Designer',
			'Product Manager'
		],
		description: 'Tasks grouped into Sprints',
		icon: '💻'
	},
	marketing: {
		type: 'marketing',
		displayName: 'Marketing',
		groupTerm: 'Campaign',
		groupTermPlural: 'Campaigns',
		taskTerm: 'Deliverable',
		taskTermPlural: 'Deliverables',
		memberTerm: 'Member',
		statusOptions: ['Planned', 'In Progress', 'In Review', 'Published', 'Done'],
		defaultRoles: [
			'Campaign Manager',
			'Copywriter',
			'Designer',
			'SEO Specialist',
			'Social Media Manager',
			'Analyst'
		],
		description: 'Deliverables grouped into Campaigns',
		icon: '📣'
	},
	construction: {
		type: 'construction',
		displayName: 'Construction',
		groupTerm: 'Phase',
		groupTermPlural: 'Phases',
		taskTerm: 'Work Package',
		taskTermPlural: 'Work Packages',
		memberTerm: 'Crew Member',
		statusOptions: ['Planned', 'Mobilising', 'In Progress', 'Inspection', 'Complete'],
		defaultRoles: [
			'Project Manager',
			'Site Engineer',
			'Architect',
			'Safety Officer',
			'Electrician',
			'Structural Engineer'
		],
		description: 'Work Packages grouped into Phases',
		icon: '🏗️'
	},
	journalism: {
		type: 'journalism',
		displayName: 'Journalism',
		groupTerm: 'Assignment',
		groupTermPlural: 'Assignments',
		taskTerm: 'Story',
		taskTermPlural: 'Stories',
		memberTerm: 'Reporter',
		statusOptions: ['Pitch', 'Researching', 'Writing', 'Editing', 'Published'],
		defaultRoles: ['Editor', 'Reporter', 'Photographer', 'Fact-checker', 'Copy Editor', 'Producer'],
		description: 'Stories grouped into Assignments',
		icon: '📰'
	},
	electrical: {
		type: 'electrical',
		displayName: 'Electrical Engineering',
		groupTerm: 'Stage',
		groupTermPlural: 'Stages',
		taskTerm: 'Work Order',
		taskTermPlural: 'Work Orders',
		memberTerm: 'Technician',
		statusOptions: ['Planned', 'Scheduled', 'In Progress', 'Testing', 'Commissioned'],
		defaultRoles: [
			'Electrical Engineer',
			'Technician',
			'Inspector',
			'Project Manager',
			'CAD Designer',
			'Safety Officer'
		],
		description: 'Work Orders grouped into Stages',
		icon: '⚡'
	},
	business: {
		type: 'business',
		displayName: 'Business / Operations',
		groupTerm: 'Initiative',
		groupTermPlural: 'Initiatives',
		taskTerm: 'Action Item',
		taskTermPlural: 'Action Items',
		memberTerm: 'Member',
		statusOptions: ['Proposed', 'Approved', 'In Progress', 'On Hold', 'Closed'],
		defaultRoles: [
			'Operations Manager',
			'Business Analyst',
			'Stakeholder',
			'Finance Lead',
			'HR Lead',
			'Executive Sponsor'
		],
		description: 'Action Items grouped into Initiatives',
		icon: '📊'
	},
	general: {
		type: 'general',
		displayName: 'General',
		groupTerm: 'Phase',
		groupTermPlural: 'Phases',
		taskTerm: 'Task',
		taskTermPlural: 'Tasks',
		memberTerm: 'Member',
		statusOptions: ['Todo', 'In Progress', 'Done'],
		defaultRoles: ['Lead', 'Member', 'Contributor'],
		description: 'Tasks grouped into Phases',
		icon: '📋'
	}
};

export const currentWorkspace = writable<CurrentWorkspace | null>(null);

function normalizeProjectType(value: string | undefined | null): ProjectType {
	const normalized = (value ?? '').trim().toLowerCase() as ProjectType;
	return normalized in PROJECT_TYPE_CONFIGS ? normalized : 'general';
}

export const projectTypeConfig = derived(currentWorkspace, ($workspace) => {
	return PROJECT_TYPE_CONFIGS[normalizeProjectType($workspace?.project_type)];
});
