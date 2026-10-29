// At most `limit` tasks at once, the rest started in the order they asked.

export interface Limiter {
  run<T>(task: () => Promise<T>): Promise<T>;
  readonly active: number;
  readonly waiting: number;
}

export function createLimiter(limit: number): Limiter {
  if (!Number.isInteger(limit) || limit < 1) throw new RangeError("concurrency must be a positive integer");
  let active = 0;
  const queue: Array<() => void> = [];

  const release = (): void => {
    active -= 1;
    const next = queue.shift();
    if (next) {
      active += 1;
      next();
    }
  };

  return {
    get active() {
      return active;
    },
    get waiting() {
      return queue.length;
    },
    async run<T>(task: () => Promise<T>): Promise<T> {
      if (active < limit) {
        active += 1;
      } else {
        await new Promise<void>((resolve) => queue.push(resolve));
      }
      try {
        return await task();
      } finally {
        release();
      }
    },
  };
}
