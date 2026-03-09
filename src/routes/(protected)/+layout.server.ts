import { redirect } from '@sveltejs/kit';
import type { LayoutServerLoad } from './$types';

export const load: LayoutServerLoad = async ({ locals, url }) => {
	if (!locals.user) {
		const redirectTarget = `${url.pathname}${url.search}`;
		throw redirect(303, `/login?redirect=${encodeURIComponent(redirectTarget)}`);
	}

	return {
		user: locals.user
	};
};
