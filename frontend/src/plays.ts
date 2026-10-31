/** playFinished matches plays.go finished: watched to the end, credits included. */
export function playFinished(position: number, duration: number): boolean {
  return duration > 0 && (position >= 0.98 * duration || duration - position <= 10);
}

/** playHasProgress is a bar worth drawing: past the first seconds of a known length. */
export function playHasProgress(position: number, duration: number): boolean {
  return duration > 0 && position >= 5;
}
