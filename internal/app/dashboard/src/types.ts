export type RuntimeState = 'running' | 'stopped' | 'degraded';

export interface ComponentStatus {
  state: RuntimeState;
  pid?: number;
  port?: number;
}

export interface RuntimeStatus {
  state: RuntimeState;
  proxy: ComponentStatus;
  bridge?: ComponentStatus;
}

export interface DialectView {
  name: string;
  preset: string;
  provider: string;
  model: string;
  subagentModel?: string;
  opusModel?: string;
  sonnetModel?: string;
  haikuModel?: string;
  effort: boolean;
  effortLevel?: string;
  concurrency: number;
  toolSearch: boolean;
  port: number;
  baseUrl?: string;
  authTokenEnv?: string;
  authProvider?: string;
  bridge?: string;
  bridgePort?: number;
  extraEnvKeys?: string[];
  status?: RuntimeStatus;
}

export interface DialectInput {
  name: string;
  preset: string;
  model: string;
  subagentModel: string;
  opusModel: string;
  sonnetModel: string;
  haikuModel: string;
  effortLevel: string;
  concurrency: number;
  port: number;
  bridgePort: number;
  baseUrl: string;
  authTokenEnv: string;
  effort: boolean;
  toolSearch: boolean;
}

export interface BootstrapResponse {
  version: string;
  url: string;
  csrfToken: string;
}

export interface DialectListResponse {
  dialects: DialectView[];
  revision: string;
}

export interface PresetListResponse {
  presets: DialectView[];
}

export interface DialectMutationResponse {
  dialect: DialectView;
  created: boolean;
  revision: string;
}

export interface NativeLauncherView {
  name: string;
  path: string;
  claudePath: string;
  dangerous: boolean;
  verified: boolean;
}

export interface NativeLauncherInput {
  name: string;
  directory: string;
  dangerous: boolean;
}

export interface NativeLauncherListResponse {
  launchers: NativeLauncherView[];
  revision: string;
}

export interface NativeLauncherMutationResponse {
  launcher: NativeLauncherView;
  revision: string;
}

export interface CursorRuntimeStatus {
  nodePath?: string;
  nodeVersion?: string;
  nodeError?: string;
  runtimeInstalled: boolean;
  runtimeCurrent: boolean;
  installedVersion?: string;
  requiredVersion: string;
  apiKeySet: boolean;
}

export interface CursorInstallResult {
  nodePath: string;
  nodeVersion: string;
  installedVersion: string;
  stoppedDialects: string[];
}

export interface DashboardErrorEnvelope {
  error: {
    code: string;
    message: string;
  };
}
