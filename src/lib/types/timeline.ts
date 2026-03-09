export type TimelineTaskStatus = 'todo' | 'in_progress' | 'done';
export type TimelineTaskDurationUnit = 'hours' | 'days';

export interface TimelineTask {
	id: string;
	title: string;
	status: TimelineTaskStatus;
	effort_score: number;
	type: string;
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
	tasks: TimelineTask[];
}

export interface ProjectTimeline {
	project_name: string;
	tech_stack?: string[];
	target_audience?: string;
	estimated_cost?: string;
	roles_needed?: string[];
	is_partial?: boolean;
	missing_sprints?: string[];
	total_progress: number;
	sprints: Sprint[];
}
