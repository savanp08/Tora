declare global {
	namespace App {
		type AuthUser = {
			id: string;
			email: string;
			name: string;
			avatarUrl: string;
			role: 'admin' | 'member' | 'viewer';
		};

		interface Locals {
			user: AuthUser | null;
		}

		interface PageData {
			user?: AuthUser;
		}
	}
}

export {};
