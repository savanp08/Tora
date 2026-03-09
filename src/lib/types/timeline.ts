export type TimelineTaskStatus = 'todo' | 'in_progress' | 'done';

export interface TimelineTask {
	id: string;
	title: string;
	status: TimelineTaskStatus;
	effort_score: number;
	type: string;
	description?: string;
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
	total_progress: number;
	sprints: Sprint[];
}
