package workflow

// preludeSource runs once per VM before any script. It evaluates to a factory
// that takes the Go helpers it needs and returns parallel() and pipeline(), so
// none of those helpers leak into the script's global scope.
//
// It also removes the clock and randomness. A resumed run replays agent()
// results by matching each call's prompt and options; a script that varied
// its prompts by time or chance would miss on every replay.
const preludeSource = `(function (checkItems, dropped, consoleLog, maxItems) {
  'use strict';

  const unavailable = (what) => function () {
    throw new Error(what + ' is unavailable in workflow scripts: resume replays agent() results by their prompts, so a script must not vary by time or chance. Pass timestamps or seeds in through args.');
  };
  const RealDate = Date;
  function WorkflowDate(...values) {
    if (!new.target) {
      unavailable('Date() called as a function')();
    }
    if (values.length === 0) {
      unavailable('new Date() without arguments')();
    }
    return new RealDate(...values);
  }
  WorkflowDate.prototype = RealDate.prototype;
  WorkflowDate.UTC = RealDate.UTC;
  WorkflowDate.parse = RealDate.parse;
  WorkflowDate.now = unavailable('Date.now()');
  globalThis.Date = WorkflowDate;
  Math.random = unavailable('Math.random()');

  globalThis.console = Object.freeze({
    log: consoleLog,
    info: consoleLog,
    warn: consoleLog,
    error: consoleLog,
    debug: consoleLog,
  });

  // parallel is a barrier: it settles once every task has. A task that throws
  // becomes null instead of rejecting the whole call.
  const parallel = async (tasks) => {
    if (!Array.isArray(tasks)) {
      throw new TypeError('parallel() needs an array of functions, such as items.map(item => () => agent(...))');
    }
    checkItems('parallel', tasks.length);
    return Promise.all(tasks.map(async (task, index) => {
      try {
        return await (typeof task === 'function' ? task() : task);
      } catch (thrown) {
        dropped('parallel', index, thrown);
        return null;
      }
    }));
  };

  // pipeline runs each item through every stage on its own, with no barrier
  // between stages. A stage that throws drops its item to null and skips the
  // item's remaining stages.
  const pipeline = async (items, ...stages) => {
    if (!Array.isArray(items)) {
      throw new TypeError('pipeline() needs an array of items as its first argument');
    }
    checkItems('pipeline', items.length);
    stages.forEach((stage, index) => {
      if (typeof stage !== 'function') {
        throw new TypeError('pipeline() stage ' + (index + 1) + ' is not a function');
      }
    });
    return Promise.all(items.map(async (item, index) => {
      let value = item;
      for (const stage of stages) {
        try {
          value = await stage(value, item, index);
        } catch (thrown) {
          dropped('pipeline', index, thrown);
          return null;
        }
      }
      return value;
    }));
  };

  return { parallel, pipeline };
})`
