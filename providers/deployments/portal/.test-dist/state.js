export function initialReadState(empty) {
    return { data: empty, phase: 'idle', loading: false, error: null, retryable: true };
}
export function beginRead(state) {
    return {
        ...state,
        phase: state.phase === 'loaded' || state.phase === 'stale' ? state.phase : 'loading',
        loading: true,
        error: null,
    };
}
export function completeRead(data) {
    return { data, phase: 'loaded', loading: false, error: null, retryable: true };
}
export function failRead(state, message, retryable = true) {
    const hasData = state.phase === 'loaded' || state.phase === 'stale';
    return {
        ...state,
        phase: hasData ? 'stale' : 'error',
        loading: false,
        error: message,
        retryable,
    };
}
export function readStatusText(phase, hasData) {
    if (phase === 'loading' && !hasData)
        return 'Loading deployments…';
    if (phase === 'stale')
        return 'Showing the last successful result; refresh failed.';
    if (phase === 'error')
        return 'Deployments are unavailable.';
    if (phase === 'loaded' && !hasData)
        return 'No deployments have been projected into this workspace.';
    return '';
}
export function readErrorMessage(error, fallback) {
    const reason = typeof error === 'object' && error !== null && 'reason' in error
        ? String(error.reason ?? '')
        : '';
    const detail = error instanceof Error ? error.message : '';
    switch (reason) {
        case 'Unauthorized':
            return 'Workspace access is unauthorized. Sign in again or choose a workspace with Deployments enabled.';
        case 'MissingBackend':
            return 'Deployments resources are not available in this workspace. Enable Deployments and its Infrastructure dependency, then retry.';
        case 'NotFound':
            return detail || 'The requested Deployment was not found in this workspace.';
        case 'NetworkError':
            return 'The workspace gateway is unavailable. Retry the read.';
        case 'ProtocolError':
            return detail || 'The workspace gateway returned incomplete evidence. Retry the read.';
        default:
            return detail || fallback;
    }
}
