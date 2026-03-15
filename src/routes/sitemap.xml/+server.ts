import type { RequestHandler } from './$types';

const INDEXABLE_PATHS = ['/', '/home', '/ide'] as const;

function escapeXml(value: string) {
	return value
		.replace(/&/g, '&amp;')
		.replace(/</g, '&lt;')
		.replace(/>/g, '&gt;')
		.replace(/"/g, '&quot;')
		.replace(/'/g, '&apos;');
}

export const GET: RequestHandler = async ({ url }) => {
	const lastModifiedDate = new Date().toISOString().slice(0, 10);
	const body = `<?xml version="1.0" encoding="UTF-8"?>
<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">
${INDEXABLE_PATHS.map((pathname) => {
	const absolute = `${url.origin}${pathname === '/' ? '' : pathname}`;
	return `  <url>
    <loc>${escapeXml(absolute)}</loc>
    <lastmod>${lastModifiedDate}</lastmod>
    <changefreq>daily</changefreq>
    <priority>${pathname === '/' ? '1.0' : '0.8'}</priority>
  </url>`;
}).join('\n')}
</urlset>`;

	return new Response(body, {
		headers: {
			'Content-Type': 'application/xml; charset=utf-8',
			'Cache-Control': 'public, max-age=3600'
		}
	});
};
