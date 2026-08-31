import { readFileSync, writeFileSync } from "node:fs";

const check = process.argv.includes("--check");

const version = readFileSync("internal/version/version", "utf8").trim();
if (!/^\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?$/.test(version)) {
  throw new Error(`invalid Hoomail version: ${JSON.stringify(version)}`);
}

const chartPath = "charts/hoomail/Chart.yaml";
const currentChart = readFileSync(chartPath, "utf8");
const chart = currentChart
  .replace(/^version: .*$/m, `version: ${version}`)
  .replace(/^appVersion: .*$/m, `appVersion: "${version}"`);

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

if (check) {
  if (chart !== currentChart || readme !== currentReadme) {
    throw new Error("release version synchronization is stale");
  }
} else {
  writeFileSync(chartPath, chart);
  writeFileSync(readmePath, readme);
}
