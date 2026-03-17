declare module 'tabulator-tables' {
	export const Tabulator: new (
		element: HTMLElement,
		options: Record<string, unknown>
	) => {
		updateData(data: unknown[]): Promise<unknown>;
		addData?(data: unknown[], addToTop?: boolean): Promise<unknown>;
		deleteRow?(id: string): Promise<unknown> | void;
		destroy(): void;
		on(event: string, callback: (cell: unknown) => void): void;
	};

	export const TabulatorFull: new (
		element: HTMLElement,
		options: Record<string, unknown>
	) => {
		updateData(data: unknown[]): Promise<unknown>;
		addData?(data: unknown[], addToTop?: boolean): Promise<unknown>;
		deleteRow?(id: string): Promise<unknown> | void;
		destroy(): void;
		on(event: string, callback: (cell: unknown) => void): void;
	};
}
