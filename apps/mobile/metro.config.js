const path = require("path");
const { getDefaultConfig } = require("expo/metro-config");

const projectRoot = __dirname;
const workspaceRoot = path.resolve(projectRoot, "../..");
const projectNodeModules = path.resolve(projectRoot, "node_modules");
const workspaceNodeModules = path.resolve(workspaceRoot, "node_modules");
const config = getDefaultConfig(projectRoot);

function resolveMobileModule(moduleName) {
  return require.resolve(moduleName, {
    paths: [projectNodeModules, workspaceNodeModules],
  });
}

function resolveMobilePackage(packageName) {
  return path.dirname(resolveMobileModule(`${packageName}/package.json`));
}

config.watchFolders = [workspaceRoot];
config.resolver.nodeModulesPaths = [
  projectNodeModules,
  workspaceNodeModules,
];
config.resolver.resolveRequest = (context, moduleName, platform) => {
  if (moduleName === "react" || moduleName.startsWith("react/")) {
    return { type: "sourceFile", filePath: resolveMobileModule(moduleName) };
  }
  if (moduleName === "react-native" || moduleName.startsWith("react-native/")) {
    return { type: "sourceFile", filePath: resolveMobileModule(moduleName) };
  }
  return context.resolveRequest(context, moduleName, platform);
};
config.resolver.extraNodeModules = {
  ...(config.resolver.extraNodeModules ?? {}),
  react: resolveMobilePackage("react"),
  "react-native": resolveMobilePackage("react-native"),
};

module.exports = config;
