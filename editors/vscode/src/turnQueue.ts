/**
 * Pending turns for one conversation. A priority turn is next, but replacing
 * it never loses the earlier request: that request returns to the FIFO front.
 */
export class TurnQueue<T> {
  private readonly items: T[] = [];
  private urgent: T | undefined;

  get count(): number {
    return this.items.length + (this.urgent === undefined ? 0 : 1);
  }

  get interrupting(): boolean {
    return this.urgent !== undefined;
  }

  enqueue(value: T): void {
    this.items.push(value);
  }

  prioritize(value: T): void {
    if (this.urgent !== undefined) this.items.unshift(this.urgent);
    this.urgent = value;
  }

  next(): T | undefined {
    if (this.urgent !== undefined) {
      const value = this.urgent;
      this.urgent = undefined;
      return value;
    }
    return this.items.shift();
  }

  clear(): void {
    this.items.length = 0;
    this.urgent = undefined;
  }
}
