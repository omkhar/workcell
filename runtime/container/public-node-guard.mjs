import crypto from 'node:crypto';
import fs from 'node:fs';
import path from 'node:path';
import { registerHooks } from 'node:module';
import { fileURLToPath } from 'node:url';

const PROTECTED_PACKAGES = new Set([
  '@google/gemini-cli',
]);

const PROTECTED_PACKAGE_ROOTS = new Map([
  [
    '@google/gemini-cli',
    '/opt/workcell/providers/node_modules/@google/gemini-cli',
  ],
]);

const PROTECTED_PACKAGE_COPY_MATCH_THRESHOLD = 6;
const PROTECTED_PACKAGE_TOKEN_MATCH_THRESHOLD = 24;
const PROTECTED_ENTRYPOINT_TOKEN_MATCH_THRESHOLD = 12;

const fileHashCache = new Map();
const fileTextCache = new Map();
const packageNameCache = new Map();
const packageRootCache = new Map();
const packageMarkerCache = new Map();
const packageJsonSignatureCache = new Map();
const packageProtectedSymlinkCache = new Map();
const packageScriptTokenCache = new Map();
const protectedPackageManifestCache = new Map();
const protectedPackageJsonSignatureCache = new Map();
const protectedPackageTokenCache = new Map();
const protectedEntrypointTokenCache = new Map();
const protectedEntrypointPathCache = new Map();
const protectedEntrypointDigestCache = new Map();
const entrypointMarkerCache = new Map();
const SELF_GUARD_PATH = canonicalizeFilePath(fileURLToPath(import.meta.url));
const WORKSPACE_ROOT = '/workspace';
const PROTECTED_ENTRYPOINT_TEXT_MARKERS = new Map([
  [
    '@google/gemini-cli',
    [
      'Copyright 2025 Google LLC',
      "Cannot resize a pty that has already exited",
      'Cleanup timed out, forcing exit...',
      "An unexpected critical error occurred",
    ],
  ],
]);

function evidenceCacheKey(rootPath, recursive) {
  return `${recursive ? 'recursive' : 'shallow'}:${rootPath}`;
}

function canonicalizeFilePath(candidate) {
  try {
    return fs.realpathSync.native(candidate);
  } catch {
    return path.resolve(candidate);
  }
}

function filePathFromUrl(url) {
  if (typeof url !== 'string' || !url.startsWith('file:')) {
    return null;
  }

  return canonicalizeFilePath(fileURLToPath(url));
}

function sha256File(filePath) {
  let digest = fileHashCache.get(filePath);
  if (digest !== undefined) {
    return digest;
  }

  try {
    digest = crypto
      .createHash('sha256')
      .update(fs.readFileSync(filePath))
      .digest('hex');
  } catch {
    digest = null;
  }

  fileHashCache.set(filePath, digest);
  return digest;
}

function readFileText(filePath) {
  let text = fileTextCache.get(filePath);
  if (text !== undefined) {
    return text;
  }

  try {
    text = fs.readFileSync(filePath, 'utf8');
  } catch {
    text = null;
  }

  fileTextCache.set(filePath, text);
  return text;
}

function nearestPackageRoot(filePath) {
  let currentDir = path.dirname(filePath);

  while (true) {
    const cached = packageRootCache.get(currentDir);
    if (cached !== undefined) {
      return cached;
    }

    const packageJsonPath = path.join(currentDir, 'package.json');
    try {
      if (fs.statSync(packageJsonPath).isFile()) {
        packageRootCache.set(currentDir, currentDir);
        return currentDir;
      }
    } catch (error) {
      if (error?.code !== 'ENOENT') {
        packageRootCache.set(currentDir, null);
        return null;
      }
    }

    const parentDir = path.dirname(currentDir);
    if (parentDir === currentDir) {
      packageRootCache.set(currentDir, null);
      return null;
    }

    currentDir = parentDir;
  }
}

function packageNameForRoot(packageRoot) {
  if (packageRoot === null) {
    return null;
  }

  const cached = packageNameCache.get(packageRoot);
  if (cached !== undefined) {
    return cached;
  }

  try {
    const parsed = JSON.parse(
      fs.readFileSync(path.join(packageRoot, 'package.json'), 'utf8'),
    );
    const packageName = typeof parsed.name === 'string' ? parsed.name : null;
    packageNameCache.set(packageRoot, packageName);
    return packageName;
  } catch {
    packageNameCache.set(packageRoot, null);
    return null;
  }
}

function normalizeJsonValue(value) {
  if (Array.isArray(value)) {
    return value.map((entry) => normalizeJsonValue(entry));
  }

  if (value && typeof value === 'object') {
    const normalized = {};
    for (const key of Object.keys(value).sort()) {
      if (value[key] !== undefined) {
        normalized[key] = normalizeJsonValue(value[key]);
      }
    }
    return normalized;
  }

  return value;
}

function packageJsonSignatureForRoot(packageRoot) {
  if (packageRoot === null) {
    return null;
  }

  const cached = packageJsonSignatureCache.get(packageRoot);
  if (cached !== undefined) {
    return cached;
  }

  try {
    const parsed = JSON.parse(
      fs.readFileSync(path.join(packageRoot, 'package.json'), 'utf8'),
    );

    if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) {
      packageJsonSignatureCache.set(packageRoot, null);
      return null;
    }

    const normalized = { ...parsed };
    delete normalized.name;

    const digest = crypto
      .createHash('sha256')
      .update(JSON.stringify(normalizeJsonValue(normalized)))
      .digest('hex');

    packageJsonSignatureCache.set(packageRoot, digest);
    return digest;
  } catch {
    packageJsonSignatureCache.set(packageRoot, null);
    return null;
  }
}

function protectedPackageJsonSignature(packageName) {
  const cached = protectedPackageJsonSignatureCache.get(packageName);
  if (cached !== undefined) {
    return cached;
  }

  const protectedRoot = PROTECTED_PACKAGE_ROOTS.get(packageName);
  const digest = packageJsonSignatureForRoot(
    typeof protectedRoot === 'string' ? protectedRoot : null,
  );
  protectedPackageJsonSignatureCache.set(packageName, digest);
  return digest;
}

function packageScriptTokens(rootDir, recursive = true) {
  if (rootDir === null) {
    return null;
  }

  const cacheKey = evidenceCacheKey(rootDir, recursive);
  const cached = packageScriptTokenCache.get(cacheKey);
  if (cached !== undefined) {
    return cached;
  }

  const tokens = new Set();
  for (const filePath of collectFileEntries(rootDir, recursive)) {
    if (!/\.(?:[cm]?js|mjs)$/i.test(filePath)) {
      continue;
    }

    const text = readFileText(filePath);
    if (text === null) {
      continue;
    }

    for (const token of extractSignatureTokens(text)) {
      tokens.add(token);
    }
  }

  packageScriptTokenCache.set(cacheKey, tokens);
  return tokens;
}

function protectedPackageTokens(packageName) {
  const cached = protectedPackageTokenCache.get(packageName);
  if (cached !== undefined) {
    return cached;
  }

  const protectedRoot = PROTECTED_PACKAGE_ROOTS.get(packageName);
  const tokens = packageScriptTokens(
    typeof protectedRoot === 'string' ? protectedRoot : null,
  );
  protectedPackageTokenCache.set(packageName, tokens);
  return tokens;
}

function packageTokenOverlap(rootDir, packageName, recursive = true) {
  const protectedTokens = protectedPackageTokens(packageName);
  const candidateTokens = packageScriptTokens(rootDir, recursive);
  if (
    protectedTokens === null ||
    protectedTokens.size === 0 ||
    candidateTokens === null ||
    candidateTokens.size === 0
  ) {
    return 0;
  }

  let matchedTokens = 0;
  for (const token of candidateTokens) {
    if (protectedTokens.has(token)) {
      matchedTokens += 1;
    }
  }

  return matchedTokens;
}

function extractSignatureTokens(text) {
  const tokens = new Set();
  const matches = text.match(/[A-Za-z0-9_./:-]{12,}/g) ?? [];

  for (const token of matches) {
    tokens.add(token);
  }

  return tokens;
}

function protectedEntrypointTokens(packageName) {
  const cached = protectedEntrypointTokenCache.get(packageName);
  if (cached !== undefined) {
    return cached;
  }

  const protectedRoot = PROTECTED_PACKAGE_ROOTS.get(packageName);
  const entrypointPath = protectedEntrypointPath(packageName);
  if (typeof protectedRoot !== 'string' || typeof entrypointPath !== 'string') {
    protectedEntrypointTokenCache.set(packageName, null);
    return null;
  }

  const text = readFileText(path.join(protectedRoot, entrypointPath));
  if (text === null) {
    protectedEntrypointTokenCache.set(packageName, null);
    return null;
  }

  const tokens = extractSignatureTokens(text);
  protectedEntrypointTokenCache.set(packageName, tokens);
  return tokens;
}

function entrypointTokenOverlap(packageName, text) {
  const protectedTokens = protectedEntrypointTokens(packageName);
  if (protectedTokens === null || protectedTokens.size === 0) {
    return 0;
  }

  let matchedTokens = 0;
  for (const token of extractSignatureTokens(text)) {
    if (protectedTokens.has(token)) {
      matchedTokens += 1;
    }
  }

  return matchedTokens;
}

function protectedEntrypointPath(packageName) {
  const cached = protectedEntrypointPathCache.get(packageName);
  if (cached !== undefined) {
    return cached;
  }

  const protectedRoot = PROTECTED_PACKAGE_ROOTS.get(packageName);
  if (typeof protectedRoot !== 'string') {
    protectedEntrypointPathCache.set(packageName, null);
    return null;
  }

  try {
    const parsed = JSON.parse(
      fs.readFileSync(path.join(protectedRoot, 'package.json'), 'utf8'),
    );
    const bin = parsed?.bin;
    let entrypointPath = null;

    if (typeof bin === 'string') {
      entrypointPath = bin;
    } else if (bin && typeof bin === 'object' && !Array.isArray(bin)) {
      if (typeof bin.gemini === 'string') {
        entrypointPath = bin.gemini;
      } else {
        const values = Object.values(bin).filter(
          (value) => typeof value === 'string',
        );
        if (values.length === 1) {
          entrypointPath = values[0];
        }
      }
    }

    protectedEntrypointPathCache.set(packageName, entrypointPath);
    return entrypointPath;
  } catch {
    protectedEntrypointPathCache.set(packageName, null);
    return null;
  }
}

function protectedEntrypointDigest(packageName) {
  const cached = protectedEntrypointDigestCache.get(packageName);
  if (cached !== undefined) {
    return cached;
  }

  const protectedRoot = PROTECTED_PACKAGE_ROOTS.get(packageName);
  const entrypointPath = protectedEntrypointPath(packageName);
  if (typeof protectedRoot !== 'string' || typeof entrypointPath !== 'string') {
    protectedEntrypointDigestCache.set(packageName, null);
    return null;
  }

  const digest = sha256File(path.join(protectedRoot, entrypointPath));
  protectedEntrypointDigestCache.set(packageName, digest);
  return digest;
}

function isWithinTree(candidate, root) {
  return candidate === root || candidate.startsWith(`${root}${path.sep}`);
}

function isWithinWorkspace(filePath) {
  return isWithinTree(filePath, WORKSPACE_ROOT);
}

function providerEvidenceRoot(filePath) {
  const packageRoot = nearestPackageRoot(filePath);
  if (packageRoot !== null) {
    return packageRoot;
  }

  if (isWithinWorkspace(filePath)) {
    return path.dirname(filePath);
  }

  return null;
}

function collectFileEntries(rootDir, recursive = true) {
  const entries = [];
  const pending = [rootDir];

  while (pending.length > 0) {
    const currentDir = pending.pop();
    let dirEntries = [];

    try {
      dirEntries = fs.readdirSync(currentDir, { withFileTypes: true });
    } catch {
      continue;
    }

    dirEntries.sort((left, right) => left.name.localeCompare(right.name));
    for (const entry of dirEntries) {
      const entryPath = path.join(currentDir, entry.name);

      if (entry.isSymbolicLink()) {
        continue;
      }
      if (entry.isDirectory()) {
        if (recursive) {
          pending.push(entryPath);
        }
        continue;
      }
      if (!entry.isFile()) {
        continue;
      }

      entries.push(entryPath);
    }
  }

  return entries;
}

function symlinkTargetPath(linkPath) {
  try {
    return canonicalizeFilePath(
      path.resolve(path.dirname(linkPath), fs.readlinkSync(linkPath)),
    );
  } catch {
    return null;
  }
}

function containsProtectedSymlink(rootDir, recursive = true) {
  if (!recursive) {
    return false;
  }

  const cacheKey = evidenceCacheKey(rootDir, recursive);
  const cached = packageProtectedSymlinkCache.get(cacheKey);
  if (cached !== undefined) {
    return cached;
  }

  const pending = [rootDir];
  while (pending.length > 0) {
    const currentDir = pending.pop();
    let dirEntries = [];

    try {
      dirEntries = fs.readdirSync(currentDir, { withFileTypes: true });
    } catch {
      continue;
    }

    dirEntries.sort((left, right) => left.name.localeCompare(right.name));
    for (const entry of dirEntries) {
      const entryPath = path.join(currentDir, entry.name);

      if (entry.isSymbolicLink()) {
        const targetPath = symlinkTargetPath(entryPath);
        if (targetPath !== null) {
          for (const protectedRoot of PROTECTED_PACKAGE_ROOTS.values()) {
            if (isWithinTree(targetPath, protectedRoot)) {
              packageProtectedSymlinkCache.set(cacheKey, true);
              return true;
            }
          }
        }
        continue;
      }

      if (entry.isDirectory()) {
        pending.push(entryPath);
      }
    }
  }

  packageProtectedSymlinkCache.set(cacheKey, false);
  return false;
}

function protectedPackageManifest(packageName) {
  const cached = protectedPackageManifestCache.get(packageName);
  if (cached !== undefined) {
    return cached;
  }

  const protectedRoot = PROTECTED_PACKAGE_ROOTS.get(packageName);
  if (typeof protectedRoot !== 'string') {
    protectedPackageManifestCache.set(packageName, null);
    return null;
  }

  const manifest = new Map();
  for (const filePath of collectFileEntries(protectedRoot)) {
    const relativePath = path.relative(protectedRoot, filePath);
    const digest = sha256File(filePath);

    if (digest === null) {
      continue;
    }

    manifest.set(relativePath, digest);
  }

  protectedPackageManifestCache.set(packageName, manifest);
  return manifest;
}

function looksLikeProtectedProviderCopy(rootDir, recursive = true) {
  const cacheKey = evidenceCacheKey(rootDir, recursive);
  const cached = packageMarkerCache.get(cacheKey);
  if (cached !== undefined) {
    return cached;
  }

  if (containsProtectedSymlink(rootDir, recursive)) {
    packageMarkerCache.set(cacheKey, true);
    return true;
  }

  const candidatePackageJsonSignature = packageJsonSignatureForRoot(rootDir);

  for (const packageName of PROTECTED_PACKAGES.values()) {
    const protectedPackageSignature = protectedPackageJsonSignature(packageName);
    if (
      candidatePackageJsonSignature !== null &&
      protectedPackageSignature !== null &&
      candidatePackageJsonSignature === protectedPackageSignature
    ) {
      packageMarkerCache.set(cacheKey, true);
      return true;
    }

    if (
      packageTokenOverlap(rootDir, packageName, recursive) >=
      PROTECTED_PACKAGE_TOKEN_MATCH_THRESHOLD
    ) {
      packageMarkerCache.set(cacheKey, true);
      return true;
    }

    const protectedManifest = protectedPackageManifest(packageName);
    let matchedSignatures = 0;

    if (protectedManifest === null || protectedManifest.size === 0) {
      continue;
    }

    for (const filePath of collectFileEntries(rootDir, recursive)) {
      const relativePath = path.relative(rootDir, filePath);
      const protectedDigest = protectedManifest.get(relativePath);

      if (protectedDigest === undefined) {
        continue;
      }

      if (sha256File(filePath) === protectedDigest) {
        matchedSignatures += 1;
      }

      if (matchedSignatures >= PROTECTED_PACKAGE_COPY_MATCH_THRESHOLD) {
        packageMarkerCache.set(cacheKey, true);
        return true;
      }
    }
  }

  packageMarkerCache.set(cacheKey, false);
  return false;
}

function fileTextLooksProtectedEntrypoint(filePath) {
  const cached = entrypointMarkerCache.get(filePath);
  if (cached !== undefined) {
    return cached;
  }

  if (!/\.(?:[cm]?js|mjs)$/i.test(filePath)) {
    entrypointMarkerCache.set(filePath, false);
    return false;
  }

  const text = readFileText(filePath);
  if (text === null) {
    entrypointMarkerCache.set(filePath, false);
    return false;
  }

  const packageRoot = providerEvidenceRoot(filePath);
  const candidatePackageSignature = packageJsonSignatureForRoot(packageRoot);
  for (const [packageName, markers] of PROTECTED_ENTRYPOINT_TEXT_MARKERS.entries()) {
    const protectedPackageSignature = protectedPackageJsonSignature(packageName);
    if (
      candidatePackageSignature !== null &&
      protectedPackageSignature !== null &&
      candidatePackageSignature === protectedPackageSignature
    ) {
      entrypointMarkerCache.set(filePath, true);
      return true;
    }

    let matchedMarkers = 0;
    for (const marker of markers) {
      if (text.includes(marker)) {
        matchedMarkers += 1;
      }
    }

    if (matchedMarkers >= 2) {
      entrypointMarkerCache.set(filePath, true);
      return true;
    }

    if (
      entrypointTokenOverlap(packageName, text) >=
      PROTECTED_ENTRYPOINT_TOKEN_MATCH_THRESHOLD
    ) {
      entrypointMarkerCache.set(filePath, true);
      return true;
    }
  }

  entrypointMarkerCache.set(filePath, false);
  return false;
}

function isProtectedProviderFile(filePath) {
  const digest = sha256File(filePath);
  if (digest !== null) {
    for (const packageName of PROTECTED_PACKAGES.values()) {
      if (protectedEntrypointDigest(packageName) === digest) {
        return true;
      }
    }
  }
  if (fileTextLooksProtectedEntrypoint(filePath)) {
    return true;
  }

  const packageRoot = nearestPackageRoot(filePath);
  const evidenceRoot =
    packageRoot ?? (isWithinWorkspace(filePath) ? path.dirname(filePath) : null);
  if (evidenceRoot === null) {
    return false;
  }

  const packageName = packageNameForRoot(packageRoot);
  if (packageName !== null && PROTECTED_PACKAGES.has(packageName)) {
    return true;
  }

  return looksLikeProtectedProviderCopy(evidenceRoot, packageRoot !== null);
}

function maybeBlock(filePath) {
  if (filePath === null) {
    return;
  }

  if (
    filePath !== SELF_GUARD_PATH &&
    !isWithinWorkspace(filePath) &&
    !isProtectedProviderFile(filePath)
  ) {
    throw new Error(
      'Workcell blocked public node execution outside the mounted workspace.',
    );
  }

  if (isProtectedProviderFile(filePath)) {
    throw new Error(
      'Workcell blocked provider package execution via public node.',
    );
  }
}

function blockNativeAddonLoad(filePath) {
  if (filePath === null) {
    return;
  }

  throw new Error('Workcell blocked native addon loading via public node.');
}

const mainScript = process.argv[1];
if (typeof mainScript === 'string' && mainScript.length > 0 && mainScript !== '-') {
  maybeBlock(canonicalizeFilePath(mainScript));
}

if (typeof process.dlopen === 'function') {
  const realDlopen = process.dlopen.bind(process);

  process.dlopen = function patchedDlopen(module, filename, ...args) {
    if (typeof filename === 'string' && filename.length > 0) {
      const filePath = canonicalizeFilePath(filename);

      maybeBlock(filePath);
      blockNativeAddonLoad(filePath);
    }

    return realDlopen(module, filename, ...args);
  };
}

registerHooks({
  resolve(specifier, context, nextResolve) {
    const result = nextResolve(specifier, context);
    maybeBlock(filePathFromUrl(result.url));
    return result;
  },
  load(url, context, nextLoad) {
    maybeBlock(filePathFromUrl(url));
    return nextLoad(url, context);
  },
});
