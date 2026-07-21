#!/usr/bin/env node

import { existsSync, readFileSync, readdirSync, statSync } from "node:fs";
import { dirname, resolve, sep } from "node:path";
import { fileURLToPath } from "node:url";

const root = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const dashboard = resolve(root, "internal/app/dashboard");
const lockfilePath = resolve(dashboard, "package-lock.json");
const lockfile = JSON.parse(readFileSync(lockfilePath, "utf8"));

if (lockfile.lockfileVersion < 2 || !lockfile.packages) {
  throw new Error(`Unsupported npm lockfile format in ${lockfilePath}`);
}

const compare = (left, right) => (left < right ? -1 : left > right ? 1 : 0);
const packageRecords = new Map();

for (const [packagePath, lockMetadata] of Object.entries(lockfile.packages).sort(
  ([left], [right]) => compare(left, right),
)) {
  if (!packagePath || !packagePath.split("/").includes("node_modules")) {
    continue;
  }
  if (lockMetadata.dev === true || lockMetadata.devOptional === true) {
    continue;
  }

  const packageDirectory = resolve(dashboard, packagePath);
  if (!packageDirectory.startsWith(`${dashboard}${sep}`)) {
    throw new Error(`Package path escapes the dashboard directory: ${packagePath}`);
  }
  if (!existsSync(packageDirectory)) {
    if (lockMetadata.optional === true) {
      continue;
    }
    throw new Error(`Installed package directory not found: ${packageDirectory}`);
  }

  const metadataPath = resolve(packageDirectory, "package.json");
  const metadata = JSON.parse(readFileSync(metadataPath, "utf8"));
  if (!metadata.name || !metadata.version) {
    throw new Error(`Package metadata is missing name or version: ${metadataPath}`);
  }
  if (lockMetadata.version && lockMetadata.version !== metadata.version) {
    throw new Error(
      `Installed version mismatch for ${metadata.name}: lockfile has ${lockMetadata.version}, package has ${metadata.version}`,
    );
  }

  const key = `${metadata.name}\0${metadata.version}`;
  if (!packageRecords.has(key)) {
    packageRecords.set(key, {
      directory: packageDirectory,
      license: metadata.license ?? lockMetadata.license,
      metadata,
      name: metadata.name,
      version: metadata.version,
    });
  }
}

function normalizeRepository(repository) {
  const value = typeof repository === "string" ? repository : repository?.url;
  if (!value) {
    return undefined;
  }

  return value
    .replace(/^git\+/, "")
    .replace(/^git:\/\//, "https://")
    .replace(/^git@([^:]+):/, "https://$1/")
    .replace(/\.git$/, "");
}

function sourceUrl(metadata) {
  const repository = normalizeRepository(metadata.repository);
  if (repository?.startsWith("http://") || repository?.startsWith("https://")) {
    return repository;
  }
  if (typeof metadata.homepage === "string" && /^https?:\/\//.test(metadata.homepage)) {
    return metadata.homepage;
  }
  return `https://www.npmjs.com/package/${metadata.name}`;
}

function licenseLabel(license) {
  if (typeof license === "string" && license.trim()) {
    return license.trim();
  }
  if (license && typeof license.type === "string" && license.type.trim()) {
    return license.type.trim();
  }
  return undefined;
}

function authorName(author) {
  if (typeof author === "string" && author.trim()) {
    return author.replace(/\s*<[^>]+>\s*$/, "").trim();
  }
  if (author && typeof author.name === "string" && author.name.trim()) {
    return author.name.trim();
  }
  return undefined;
}

function generatedMitLicense(metadata) {
  const author = authorName(metadata.author);
  if (!author) {
    return undefined;
  }

  return `MIT License

Copyright (c) ${author}

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.`;
}

let fragment = "";
const records = [...packageRecords.values()].sort(
  (left, right) => compare(left.name, right.name) || compare(left.version, right.version),
);

for (const record of records) {
  const declaredLicense = licenseLabel(record.license);
  if (!declaredLicense) {
    throw new Error(`No license metadata found for ${record.name} ${record.version}`);
  }

  fragment += `\n## npm:${record.name} ${record.version}\n`;
  fragment += `\nSource: ${sourceUrl(record.metadata)}\n`;
  fragment += `\nLicense: ${declaredLicense}\n`;

  const licenseFiles = readdirSync(record.directory)
    .filter((name) => /^(license|licence|copying|notice)/i.test(name))
    .filter((name) => statSync(resolve(record.directory, name)).isFile())
    .sort(compare);

  if (licenseFiles.length === 0) {
    const generatedLicense = declaredLicense === "MIT" ? generatedMitLicense(record.metadata) : undefined;
    if (!generatedLicense) {
      throw new Error(
        `No license file or supported metadata fallback found for ${record.name} ${record.version}`,
      );
    }

    fragment += "\nThe installed package declares MIT but does not include a license file. The canonical terms below use its author metadata.\n";
    fragment += "\n### MIT License (generated from package metadata)\n\n```text\n";
    fragment += `${generatedLicense}\n`;
    fragment += "```\n";
    continue;
  }

  for (const filename of licenseFiles) {
    const contents = readFileSync(resolve(record.directory, filename), "utf8").trimEnd();
    fragment += `\n### ${filename}\n\n\`\`\`text\n${contents}\n\`\`\`\n`;
  }
}

process.stdout.write(fragment);
