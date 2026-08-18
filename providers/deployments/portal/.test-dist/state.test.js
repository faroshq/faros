import { beginRead, completeRead, failRead, initialReadState, readErrorMessage, readStatusText } from './state.js';
function assert(condition, message) {
    if (!condition)
        throw new Error(message);
}
const initial = initialReadState([]);
assert(initial.phase === 'idle' && !initial.loading, 'initial state was not idle');
const loading = beginRead(initial);
assert(loading.phase === 'loading' && loading.loading, 'first read did not enter loading');
const loaded = completeRead(['deployment']);
assert(loaded.phase === 'loaded' && loaded.data[0] === 'deployment', 'successful read did not retain data');
const refreshing = beginRead(loaded);
assert(refreshing.phase === 'loaded' && refreshing.loading, 'background read did not preserve loaded geometry');
const stale = failRead(refreshing, 'gateway unavailable');
assert(stale.phase === 'stale' && stale.data[0] === 'deployment', 'failed background read discarded cached data');
assert(readStatusText(stale.phase, stale.data.length > 0).includes('last successful'), 'stale state was not explicit');
const firstError = failRead(loading, 'unauthorized', false);
assert(firstError.phase === 'error' && !firstError.retryable, 'initial non-retryable error was not explicit');
assert(readStatusText('error', false).includes('unavailable'), 'initial error copy was not explicit');
assert(readErrorMessage({ reason: 'Unauthorized', message: '403' }, 'fallback').includes('unauthorized'), 'unauthorized state was not explicit');
assert(readErrorMessage({ reason: 'MissingBackend', message: 'binding missing' }, 'fallback').includes('not available'), 'missing backend state was not explicit');
const empty = completeRead([]);
assert(readStatusText(empty.phase, empty.data.length > 0).includes('No deployments'), 'empty success was not explicit');
