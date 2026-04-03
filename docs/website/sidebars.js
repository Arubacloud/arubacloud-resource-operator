// @ts-check

/** @type {import('@docusaurus/plugin-content-docs').SidebarsConfig} */
const sidebars = {
  operatorSidebar: [
    {
      type: 'doc',
      id: 'intro',
      label: 'Overview',
    },
    {
      type: 'doc',
      id: 'architecture',
      label: 'Architecture',
    },
    {
      type: 'doc',
      id: 'installation',
      label: 'Installation',
    },
    {
      type: 'category',
      label: 'CRDs',
      items: [
        {
          type: 'doc',
          id: 'crds',
          label: 'Introduction',
        },
        'Project',
        'VPC',
        'Subnet',
        'SecurityGroup',
        'SecurityRule',
        'KeyPair',
        'ElasticIP',
        'BlockStorage',
        'CloudServer',
      ],
    },
    {
      type: 'doc',
      id: 'examples',
      label: 'Examples',
    },
    {
      type: 'doc',
      id: 'contribute',
      label: 'Contribute',
    },
  ],
};

module.exports = sidebars;

