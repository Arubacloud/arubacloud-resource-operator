// @ts-check

const lightCodeTheme = require('prism-react-renderer').themes.github;
const darkCodeTheme = require('prism-react-renderer').themes.dracula;

/** @type {import('@docusaurus/types').Config} */
const config = {
  title: 'Aruba Cloud Resource Operator',
  tagline: 'Declarative Aruba Cloud resources via Kubernetes CRDs',
  favicon: 'img/logo.svg',

  url: 'https://arubacloud.github.io',
  baseUrl: '/arubacloud-resource-operator/',

  organizationName: 'Arubacloud',
  projectName: 'arubacloud-resource-operator',

  onBrokenLinks: 'warn',
  markdown: {
    mermaid: true,
    hooks: {
      onBrokenMarkdownLinks: 'ignore',
    },
  },

  i18n: {
    defaultLocale: 'en',
    locales: ['en', 'it'],
    localeConfigs: {
      en: {
        label: 'English',
        direction: 'ltr',
        htmlLang: 'en-US',
        calendar: 'gregory',
      },
      it: {
        label: 'Italiano',
        direction: 'ltr',
        htmlLang: 'it-IT',
        calendar: 'gregory',
      },
    },
  },

  presets: [
    [
      'classic',
      /** @type {import('@docusaurus/preset-classic').Options} */
      ({
        docs: {
          sidebarPath: require.resolve('./sidebars.js'),
          editUrl: 'https://github.com/Arubacloud/arubacloud-resource-operator/tree/main/docs/website/',
          routeBasePath: '/',
          versions:
            process.env.DISABLE_VERSIONING === 'true'
              ? {}
              : {
                  current: {
                    label: 'Next',
                    path: 'next',
                  },
                },
          onlyIncludeVersions:
            process.env.DISABLE_VERSIONING === 'true' ? ['current'] : undefined,
          showLastUpdateTime: true,
          showLastUpdateAuthor: true,
        },
        blog: false,
        theme: {
          customCss: require.resolve('./src/css/custom.css'),
        },
      }),
    ],
  ],

  themes: ['@docusaurus/theme-mermaid'],

  plugins: [
    [
      require.resolve('@easyops-cn/docusaurus-search-local'),
      {
        hashed: true,
        language: ['en', 'it'],
        highlightSearchTermsOnTargetPage: true,
        explicitSearchResultPath: true,
        indexBlog: false,
        indexPages: false,
        docsRouteBasePath: '/',
      },
    ],
  ],

  themeConfig:
    /** @type {import('@docusaurus/preset-classic').ThemeConfig} */
    ({
      navbar: {
        title: 'Aruba Cloud Resource Operator',
        logo: {
          alt: 'Aruba Cloud',
          src: 'img/logo.svg',
          href: 'next/intro',
        },
        items: [
          {
            type: 'docSidebar',
            sidebarId: 'operatorSidebar',
            position: 'left',
            label: 'Docs',
          },
          ...(process.env.DISABLE_VERSIONING !== 'true'
            ? [
                {
                  type: 'docsVersionDropdown',
                  position: 'right',
                },
              ]
            : []),
          {
            type: 'localeDropdown',
            position: 'right',
          },
          {
            href: 'https://github.com/Arubacloud/arubacloud-resource-operator',
            position: 'right',
            className: 'header-github-link',
            'aria-label': 'GitHub repository',
          },
        ],
        hideOnScroll: true,
      },
      footer: {
        style: 'dark',
        links: [
          {
            title: 'Community',
            items: [
              {
                label: 'GitHub',
                href: 'https://github.com/Arubacloud/arubacloud-resource-operator',
              },
              {
                label: 'Issues',
                href: 'https://github.com/Arubacloud/arubacloud-resource-operator/issues',
              },
            ],
          },
          {
            title: 'More',
            items: [
              {
                label: 'Aruba Cloud',
                href: 'https://www.arubacloud.com',
              },
              {
                label: 'Changelog',
                href: 'https://github.com/Arubacloud/arubacloud-resource-operator/releases',
              },
            ],
          },
        ],
        copyright:
          'Copyright © 2025 Aruba S.p.A. - via San Clemente, 53 - 24036 Ponte San Pietro (BG) P.IVA 01573850516 - C.F. 04552920482 - C.S. € 4.000.000,00 i.v. - Numero REA: BG – 434483 - All rights reserved',
      },
      prism: {
        theme: lightCodeTheme,
        darkTheme: darkCodeTheme,
        additionalLanguages: ['go', 'yaml', 'bash'],
      },
    }),
};

module.exports = config;

