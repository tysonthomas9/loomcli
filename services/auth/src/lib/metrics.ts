class Counter {
  private value = 0;
  constructor(
    private readonly name: string,
    private readonly help: string,
  ) {}

  inc(): void {
    this.value++;
  }

  render(): string {
    return `# HELP ${this.name} ${this.help}\n# TYPE ${this.name} counter\n${this.name} ${this.value}`;
  }
}

export const metrics = {
  signInAttempts: new Counter("auth_sign_in_attempts_total", "Total sign-in attempts"),
  sessionCreated: new Counter("auth_sessions_created_total", "Total sessions created"),
  tokenIssued: new Counter("auth_tokens_issued_total", "Total JWT tokens issued"),
  rateLimitTriggered: new Counter("auth_rate_limit_triggered_total", "Total rate limit hits"),
};

export function renderMetrics(): string {
  return Object.values(metrics)
    .map((c) => c.render())
    .join("\n\n") + "\n";
}
