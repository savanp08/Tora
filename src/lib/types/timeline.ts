export type TimelineTaskStatus = 'todo' | 'in_progress' | 'done';
export type TimelineTaskDurationUnit = 'hours' | 'days';
export type TimelineTaskPriority = 'critical' | 'high' | 'medium' | 'low';

export interface TimelineTask {
	id: string;
	title: string;
	status: TimelineTaskStatus;
	effort_score: number;
	type: string;
	priority?: TimelineTaskPriority;
	assignee?: string;
	status_actor_id?: string;
	status_actor_name?: string;
	status_changed_at?: string;
	description?: string;
	start_date?: string;
	end_date?: string;
	duration_unit?: TimelineTaskDurationUnit;
	duration_value?: number;
}

export interface Sprint {
	id: string;
	name: string;
	start_date: string;
	end_date: string;
	goal?: string;
	budget_allocated?: number;
	tasks: TimelineTask[];
}

export interface ProjectTimeline {
	project_name: string;
	description?: string;
	tech_stack?: string[];
	target_audience?: string;
	estimated_cost?: string;
	budget_total?: number;
	budget_spent?: number;
	roles_needed?: string[];
	is_partial?: boolean;
	missing_sprints?: string[];
	total_progress: number;
	sprints: Sprint[];
}
