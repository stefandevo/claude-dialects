import type {
  BootstrapResponse,
  CursorInstallResult,
  CursorRuntimeStatus,
  DashboardErrorEnvelope,
  DialectInput,
  DialectListResponse,
  DialectMutationResponse,
  DialectView,
  NativeLauncherInput,
  NativeLauncherListResponse,
  NativeLauncherMutationResponse,
  PresetListResponse,
  RuntimeStatus,
} from './types';

const API_ROOT = '/api/v1';

export class ApiError extends Error {
  constructor(
    message: string,
    readonly status: number,
    readonly code: string,
  ) {
    super(message);
    this.name = 'ApiError';
  }

  get isConflict() {
    return this.status === 412 || this.code === 'revision_conflict';
  }
}

interface RequestOptions extends RequestInit {
  revision?: string;
  mutation?: boolean;
}

interface ApiResult<T> {
  data: T;
  revision?: string;
}

function unquoteETag(value: string | null) {
  if (!value) return undefined;
  return value.startsWith('"') && value.endsWith('"') ? value.slice(1, -1) : value;
}

export class DashboardApi {
  private csrfToken = '';

  async bootstrap() {
    const result = await this.request<BootstrapResponse>('/bootstrap');
    this.csrfToken = result.data.csrfToken;
    return result.data;
  }

  presets() {
    return this.request<PresetListResponse>('/presets').then((result) => result.data.presets);
  }

  listDialects() {
    return this.request<DialectListResponse>('/dialects?status=true');
  }

  getDialect(name: string) {
    return this.request<DialectView>(`/dialects/${encodeURIComponent(name)}`);
  }

  createDialect(input: DialectInput, revision?: string) {
    return this.request<DialectMutationResponse>('/dialects', {
      method: 'POST',
      mutation: true,
      revision,
      body: JSON.stringify(input),
    });
  }

  updateDialect(name: string, input: DialectInput, revision: string) {
    return this.request<DialectMutationResponse>(`/dialects/${encodeURIComponent(name)}`, {
      method: 'PUT',
      mutation: true,
      revision,
      body: JSON.stringify(input),
    });
  }

  deleteDialect(name: string, revision: string) {
    return this.request<void>(`/dialects/${encodeURIComponent(name)}`, {
      method: 'DELETE',
      mutation: true,
      revision,
      body: JSON.stringify({ confirmation: name }),
    });
  }

  dialectAction(name: string, action: 'start' | 'stop' | 'restart') {
    return this.request<{ name: string; status: RuntimeStatus }>(
      `/dialects/${encodeURIComponent(name)}/${action}`,
      { method: 'POST', mutation: true, body: '{}' },
    ).then((result) => result.data.status);
  }

  cursorStatus() {
    return this.request<CursorRuntimeStatus>('/cursor/runtime').then((result) => result.data);
  }

  installCursorRuntime() {
    return this.request<CursorInstallResult>('/cursor/runtime', {
      method: 'PUT',
      mutation: true,
      body: '{}',
    }).then((result) => result.data);
  }

  listLaunchers() {
    return this.request<NativeLauncherListResponse>('/native-launchers');
  }

  createLauncher(input: NativeLauncherInput, revision?: string) {
    return this.request<NativeLauncherMutationResponse>('/native-launchers', {
      method: 'POST',
      mutation: true,
      revision,
      body: JSON.stringify(input),
    });
  }

  updateLauncher(name: string, input: NativeLauncherInput, revision: string) {
    return this.request<NativeLauncherMutationResponse>(`/native-launchers/${encodeURIComponent(name)}`, {
      method: 'PUT',
      mutation: true,
      revision,
      body: JSON.stringify(input),
    });
  }

  deleteLauncher(name: string, revision: string) {
    return this.request<void>(`/native-launchers/${encodeURIComponent(name)}`, {
      method: 'DELETE',
      mutation: true,
      revision,
      body: JSON.stringify({ confirmation: name }),
    });
  }

  private async request<T>(path: string, options: RequestOptions = {}): Promise<ApiResult<T>> {
    const headers = new Headers(options.headers);
    headers.set('Accept', 'application/json');
    if (options.body !== undefined) headers.set('Content-Type', 'application/json');
    if (options.mutation) {
      if (!this.csrfToken) throw new Error('Dashboard security token is not initialized.');
      headers.set('X-CC-Dialect-CSRF', this.csrfToken);
    }
    if (options.revision) headers.set('If-Match', `"${options.revision}"`);

    const response = await fetch(`${API_ROOT}${path}`, { ...options, headers });
    const revision = unquoteETag(response.headers.get('ETag'));
    if (!response.ok) {
      let envelope: DashboardErrorEnvelope | undefined;
      try {
        envelope = (await response.json()) as DashboardErrorEnvelope;
      } catch {
        // Use a safe generic message when a proxy or browser returns non-JSON.
      }
      throw new ApiError(
        envelope?.error.message || `Request failed with status ${response.status}.`,
        response.status,
        envelope?.error.code || 'request_failed',
      );
    }
    if (response.status === 204) return { data: undefined as T, revision };
    return { data: (await response.json()) as T, revision };
  }
}

export const dashboardApi = new DashboardApi();
