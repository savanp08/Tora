declare module 'tabulator-tables' {
	export const Tabulator: new (
		element: HTMLElement,
		options: Record<string, unknown>
	) => {
		replaceData(data: unknown[]): Promise<unknown>;
		destroy(): void;
		on(event: string, callback: (cell: unknown) => void): void;
	};

	export const TabulatorFull: new (
		element: HTMLElement,
		options: Record<string, unknown>
	) => {
		replaceData(data: unknown[]): Promise<unknown>;
		destroy(): void;
		on(event: string, callback: (cell: unknown) => void): void;
	};
}
