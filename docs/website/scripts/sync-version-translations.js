const fs = require('fs');
const path = require('path');

const docsWebsiteDir = path.resolve(__dirname, '..');
const versionsFile = path.resolve(docsWebsiteDir, 'versions.json');
const translationsSource = path.resolve(
  docsWebsiteDir,
  'i18n',
  'it',
  'docusaurus-plugin-content-docs',
  'current',
);
const i18nBase = path.resolve(
  docsWebsiteDir,
  'i18n',
  'it',
  'docusaurus-plugin-content-docs',
);

let versions = [];
if (fs.existsSync(versionsFile)) {
  versions = JSON.parse(fs.readFileSync(versionsFile, 'utf8'));
}
if (!Array.isArray(versions) || versions.length === 0) {
  console.log('No versions found in versions.json, nothing to sync.');
  process.exit(0);
}

const sourceFiles = fs
  .readdirSync(translationsSource, {withFileTypes: true})
  .filter(
    (dirent) =>
      dirent.isFile() && (dirent.name.endsWith('.md') || dirent.name === 'current.json'),
  )
  .map((dirent) => dirent.name);

console.log(`Syncing translations from 'current' to ${versions.length} versions...`);

versions.forEach((version) => {
  const versionDir = path.join(i18nBase, `version-${version}`);

  if (!fs.existsSync(versionDir)) {
    fs.mkdirSync(versionDir, {recursive: true});
    console.log(`Created directory: ${versionDir}`);
  }

  sourceFiles.forEach((file) => {
    const sourceFile = path.join(translationsSource, file);

    if (file === 'current.json') {
      const destFileName = `version-${version}.json`;
      const finalDestFile = path.join(versionDir, destFileName);

      const content = JSON.parse(fs.readFileSync(sourceFile, 'utf8'));
      if (content['version.label']) {
        content['version.label'].message = version;
      }

      fs.writeFileSync(finalDestFile, JSON.stringify(content, null, 2) + '\n');
      console.log(`  Copied and updated ${file} -> version-${version}/${destFileName}`);
      return;
    }

    const finalDestFile = path.join(versionDir, file);
    fs.copyFileSync(sourceFile, finalDestFile);
    console.log(`  Copied ${file} -> version-${version}/${file}`);
  });
});

console.log('Translation sync completed!');

