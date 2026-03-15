import type { LayoutServerLoad } from './$types';
import { resolveSiteOrigin } from '$lib/utils/seo';

const PUBLIC_SITE_URL_ENV = (process.env.PUBLIC_SITE_URL || '').trim();

export const load: LayoutServerLoad = async ({ url }) => {
	return {
		siteOrigin: resolveSiteOrigin(PUBLIC_SITE_URL_ENV, url.origin)
	};
};
