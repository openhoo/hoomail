import { readFileSync, writeFileSync } from "node:fs";

const check = process.argv.includes("--check");

const version = readFileSync("internal/version/version", "utf8").trim();
if (!/^\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?$/.test(version)) {
  throw new Error(`invalid Hoomail version: ${JSON.stringify(version)}`);
}

function replaceVersionSlot(
  content: string,
  pattern: RegExp,
  replacement: string,
  label: string,
): string {
  const matches = content.match(pattern) ?? [];
  if (matches.length !== 1) {
    throw new Error(`expected exactly one ${label} anchor, found ${matches.length}`);
  }
  return content.replace(pattern, replacement);
}

const chartPath = "charts/hoomail/Chart.yaml";
const currentChart = readFileSync(chartPath, "utf8");
const chartWithVersion = replaceVersionSlot(
  currentChart,
  /^version: .*$/gm,
  `version: ${version}`,
  "chart version",
);
const chart = replaceVersionSlot(
  chartWithVersion,
  /^appVersion: .*$/gm,
  `appVersion: "${version}"`,
  "chart appVersion",
);
const readmePath = "README.md";
const currentReadme = readFileSync(readmePath, "utf8");
const imagePattern = /ghcr\.io\/openhoo\/hoomail:(?:latest|\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?)/g;
const matches = currentReadme.match(imagePattern) ?? [];
if (matches.length !== 2) {
  throw new Error(`expected exactly two Hoomail image examples, found ${matches.length}`);
}
const readme = currentReadme.replace(
  imagePattern,
  `ghcr.io/openhoo/hoomail:${version}`,
);

const runtimePath = "docs/runtime.md";
const currentRuntime = readFileSync(runtimePath, "utf8");
const runtimeTranscriptPattern =
  /^(\$ hoomail version\n)\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?$/gm;
const runtimeEmbeddedVersionPattern =
  /^(`internal\/version\.Value` starts as `"dev"`. During package initialization, an unchanged `"dev"` value is replaced with the trimmed contents of the compile-time embedded \[`internal\/version\/version`\]\(\.\.\/internal\/version\/version\) file, currently )`\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?`(\. Release builds can replace `internal\/version\.Value` through Go linker flags; when replaced with a value other than `"dev"`, that linker-provided value is printed instead\.)$/gm;

function replaceRuntimeSlot(
  content: string,
  pattern: RegExp,
  replacement: string,
  label: string,
): string {
  const matches = content.match(pattern) ?? [];
  if (matches.length !== 1) {
    throw new Error(`expected exactly one ${label} anchor, found ${matches.length}`);
  }
  return content.replace(pattern, replacement);
}

let runtime = replaceRuntimeSlot(
  currentRuntime,
  runtimeTranscriptPattern,
  `$1${version}`,
  "runtime version transcript",
);
runtime = replaceRuntimeSlot(
  runtime,
  runtimeEmbeddedVersionPattern,
  `$1\`${version}\`$2`,
  "embedded runtime version",
);

if (check) {
  if (chart !== currentChart || readme !== currentReadme || runtime !== currentRuntime) {
    throw new Error("release version synchronization is stale");
  }
} else {
  writeFileSync(chartPath, chart);
  writeFileSync(readmePath, readme);
  writeFileSync(runtimePath, runtime);
}
