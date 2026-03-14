import { redirect } from '@sveltejs/kit';
import type { LayoutServerLoad } from './$types';

export const load: LayoutServerLoad = async ({ locals, url, cookies }) => {
	if (!locals.user) {
		// Keep protected pages reachable when JWT parsing is unavailable server-side,
		// as long as the auth cookie exists. Backend APIs still enforce token validity.
		const hasAuthCookie =
			Boolean(cookies.get('tora_auth')?.trim()) ||
			Boolean(cookies.get('converse_auth_token')?.trim());
		if (hasAuthCookie) {
			return {
				user: null
			};
		}
		const redirectTarget = `${url.pathname}${url.search}`;
		throw redirect(303, `/login?redirect=${encodeURIComponent(redirectTarget)}`);
	}

	return {
		user: locals.user
	};
};
