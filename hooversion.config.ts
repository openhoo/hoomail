export default {
  branches: ["main"],
  packages: [
    {
      name: "hoomail",
      path: ".",
      type: "version-file",
      manifest: "internal/version/version",
      changelog: "CHANGELOG.md",
      scopes: ["hoomail", "client", "server", "smtp", "docker", "ghcr", "image", "helm", "chart", "release"],
      dependencies: [],
    },
  ],
  hooks: {
    afterVersion: ["bun scripts/sync-chart-version.ts"],
  },
  github: {
    releases: true,
  },
};
