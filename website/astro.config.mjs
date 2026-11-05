// @ts-check
import { defineConfig } from 'astro/config';
import starlight from '@astrojs/starlight';

export default defineConfig({
  site: 'https://kucukkanat.github.io',
  base: '/bunker',
  trailingSlash: 'ignore',
  integrations: [
    starlight({
      title: 'Bunker Docs',
      description: 'Self-hosted internal app platform for non-technical teams. Secure by default.',
      logo: { src: './src/assets/logo.svg', alt: 'Bunker' },
      favicon: '/favicon.svg',
      social: [{ icon: 'github', label: 'GitHub', href: 'https://github.com/kucukkanat/bunker' }],
      editLink: { baseUrl: 'https://github.com/kucukkanat/bunker/edit/master/website/' },
      customCss: [
        '@fontsource-variable/fraunces',
        '@fontsource-variable/inter',
        '@fontsource-variable/jetbrains-mono',
        './src/styles/theme.css',
      ],
      head: [
        { tag: 'meta', attrs: { property: 'og:image', content: 'https://kucukkanat.github.io/bunker/og.png' } },
      ],
      sidebar: [
        { label: 'Start here', items: [{ autogenerate: { directory: 'docs/start' } }] },
        { label: 'For builders', items: [{ autogenerate: { directory: 'docs/build' } }] },
        { label: 'For admins', items: [{ autogenerate: { directory: 'docs/admin' } }] },
        { label: 'Security', items: [{ autogenerate: { directory: 'docs/security' } }] },
        { label: 'SDK reference', items: [{ autogenerate: { directory: 'docs/sdk' } }] },
        { label: 'CLI & MCP', items: [{ autogenerate: { directory: 'docs/cli' } }] },
        { label: 'Project', items: [{ autogenerate: { directory: 'docs/project' } }] },
      ],
    }),
  ],
});
