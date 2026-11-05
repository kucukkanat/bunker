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
      description: 'Your own private app cloud, for you, your team or your whole company. Self-hosted, sandboxed, MIT licensed.',
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
        { label: 'Start', items: [{ autogenerate: { directory: 'docs/start' } }] },
        { label: 'Deploy', items: [{ autogenerate: { directory: 'docs/deploy' } }] },
        { label: 'Sign-in', items: [{ autogenerate: { directory: 'docs/sign-in' } }] },
        { label: 'Builders guide', items: [{ autogenerate: { directory: 'docs/build' } }] },
        { label: 'SDK reference', items: [{ autogenerate: { directory: 'docs/sdk' } }] },
        { label: 'CLI', items: [{ autogenerate: { directory: 'docs/cli' } }] },
        { label: 'MCP and agents', items: [{ autogenerate: { directory: 'docs/agents' } }] },
        { label: 'Admin', items: [{ autogenerate: { directory: 'docs/admin' } }] },
        { label: 'Security', items: [{ autogenerate: { directory: 'docs/security' } }] },
        { label: 'Project', collapsed: true, items: [{ autogenerate: { directory: 'docs/project' } }] },
      ],
    }),
  ],
});
