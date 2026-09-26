// @ts-check
import { defineConfig } from 'astro/config';
import starlight from '@astrojs/starlight';
import mermaid from 'astro-mermaid';

// TODO: point this at the real domain before the first production deploy.
// It is used for canonical URLs, Open Graph tags, and the sitemap.
const site = 'https://lineage.proseria.dev';

const repo = 'https://github.com/proseria-research/lineage';

export default defineConfig({
	site,
	// Fully static output — the build is a directory of files that Cloudflare
	// serves directly. No adapter, no server runtime, no cold starts.
	output: 'static',
	// astro-mermaid must be registered before Starlight: it rewrites the
	// markdown pipeline that Starlight then consumes.
	integrations: [
		mermaid({
			theme: 'neutral',
			autoTheme: true,
			mermaidConfig: {
				flowchart: { curve: 'linear', useMaxWidth: true },
				themeVariables: { fontFamily: '"IBM Plex Mono", ui-monospace, monospace' },
			},
		}),
		starlight({
			title: 'Lineage',
			description:
				'A model registry you run yourself: what is live, what changed, where it came from and who approved it. One Go binary.',
			logo: {
				light: './src/assets/mark-light.svg',
				dark: './src/assets/mark-dark.svg',
			},
			favicon: '/favicon.svg',
			customCss: [
				'@fontsource/ibm-plex-sans/400.css',
				'@fontsource/ibm-plex-sans/500.css',
				'@fontsource/ibm-plex-sans/600.css',
				'@fontsource/ibm-plex-mono/400.css',
				'@fontsource/ibm-plex-mono/500.css',
				'./src/styles/tokens.css',
				'./src/styles/docs.css',
			],
			social: [{ icon: 'github', label: 'GitHub', href: repo }],
			editLink: { baseUrl: `${repo}/edit/main/site/` },
			lastUpdated: true,
			pagination: true,
			credits: false,
			tableOfContents: { minHeadingLevel: 2, maxHeadingLevel: 3 },
			sidebar: [
				{
					label: 'Start here',
					items: [
						{ label: 'What Lineage is', slug: 'start/what-lineage-is' },
						{ label: 'Quickstart', slug: 'start/quickstart' },
						{ label: 'Core concepts', slug: 'start/concepts' },
						{ label: 'Register your first model', slug: 'start/first-model' },
						{ label: 'Compliance & evidence', slug: 'start/compliance' },
					],
				},
				{ label: 'Registry', items: [{ autogenerate: { directory: 'guides' } }] },
				{ label: 'Delivery', items: [{ autogenerate: { directory: 'delivery' } }] },
				{ label: 'Operations', items: [{ autogenerate: { directory: 'operate' } }] },
				{ label: 'Deployment', items: [{ autogenerate: { directory: 'deploy' } }] },
				{ label: 'Clients', items: [{ autogenerate: { directory: 'clients' } }] },
			],
		}),
	],
});
