#!/usr/bin/env node

/**
 * Cleanup script to remove old versioned docs, keeping only the last N releases
 * Default: keeps last 5 releases
 * Usage: node cleanup-versions.js [--dry-run]
 * Environment: KEEP_LAST=5 (default) - number of releases to keep
 */

const fs = require('fs');
const path = require('path');
const https = require('https');

const DRY_RUN = process.argv.includes('--dry-run');

function getGitHubReleases(keepLast = 5) {
  return new Promise((resolve, reject) => {
    let repo = process.env.GITHUB_REPOSITORY || 'Arubacloud/arubacloud-resource-operator';

    const token = process.env.GITHUB_TOKEN;
    const authHeader = token ? `token ${token}` : '';

    const options = {
      hostname: 'api.github.com',
      path: `/repos/${repo}/releases`,
      method: 'GET',
      headers: {
        'User-Agent': 'Node.js',
        ...(authHeader && {'Authorization': authHeader}),
      },
    };

    https
      .get(options, (res) => {
        let data = '';

        res.on('data', (chunk) => {
          data += chunk;
        });

        res.on('end', () => {
          if (res.statusCode !== 200) {
            reject(new Error(`HTTP ${res.statusCode}`));
            return;
          }

          const releases = JSON.parse(data);

          const sortedReleases = releases
            .filter((r) => r.published_at)
            .map((r) => ({
              version: r.tag_name.replace(/^v/, ''),
            }))
            .sort((a, b) => {
              const aParts = a.version.split('.').map(Number);
              const bParts = b.version.split('.').map(Number);
              for (let i = 0; i < Math.max(aParts.length, bParts.length); i++) {
                const ap = aParts[i] || 0;
                const bp = bParts[i] || 0;
                if (ap !== bp) return bp - ap;
              }
              return 0;
            })
            .map((r) => r.version);

          resolve({
            all: sortedReleases,
            toKeep: sortedReleases.slice(0, keepLast),
          });
        });
      })
      .on('error', (error) => reject(error));
  });
}

function getLocalVersions() {
  const versionsFile = path.join(__dirname, 'versions.json');
  if (!fs.existsSync(versionsFile)) return [];
  return JSON.parse(fs.readFileSync(versionsFile, 'utf-8'));
}

function removeVersion(version) {
  const versionedDocsDir = path.join(__dirname, 'versioned_docs', `version-${version}`);
  const versionedSidebarsFile = path.join(
    __dirname,
    'versioned_sidebars',
    `version-${version}-sidebars.json`,
  );
  const versionsFile = path.join(__dirname, 'versions.json');

  if (DRY_RUN) return;

  if (fs.existsSync(versionedDocsDir)) fs.rmSync(versionedDocsDir, {recursive: true, force: true});
  if (fs.existsSync(versionedSidebarsFile)) fs.unlinkSync(versionedSidebarsFile);

  const versions = getLocalVersions();
  const updated = versions.filter((v) => v !== version);
  fs.writeFileSync(versionsFile, JSON.stringify(updated, null, 2) + '\n');
}

async function main() {
  const KEEP_LAST = parseInt(process.env.KEEP_LAST || '5', 10);
  const releases = await getGitHubReleases(KEEP_LAST);

  const localVersions = getLocalVersions();
  const toRemove = localVersions.filter((v) => !releases.toKeep.includes(v));
  toRemove.forEach(removeVersion);

  if (!DRY_RUN) {
    const versionsFile = path.join(__dirname, 'versions.json');
    const updated = localVersions.filter((v) => releases.toKeep.includes(v));
    fs.writeFileSync(versionsFile, JSON.stringify(updated, null, 2) + '\n');
  }
}

main().catch((err) => {
  console.error(err);
  process.exit(1);
});

