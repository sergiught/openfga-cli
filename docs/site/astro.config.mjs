// @ts-check
import { defineConfig } from 'astro/config';
import starlight from '@astrojs/starlight';

// https://astro.build/config
export default defineConfig({
	site: 'https://sergiught.github.io',
	base: '/openfga-cli',
	integrations: [
		starlight({
			title: 'ofga',
			description: 'A modern CLI & TUI for OpenFGA.',
			social: [
				{ icon: 'github', label: 'GitHub', href: 'https://github.com/sergiught/openfga-cli' },
			],
			customCss: ['./src/styles/custom.css'],
			editLink: {
				baseUrl: 'https://github.com/sergiught/openfga-cli/edit/main/docs/site/',
			},
			sidebar: [
				{ label: 'Guide', items: [{ autogenerate: { directory: 'guide' } }] },
				{ label: 'Reference', items: [{ autogenerate: { directory: 'reference' } }] },
			],
		}),
	],
});
