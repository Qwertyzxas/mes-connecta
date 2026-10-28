export type Credentials = {
  email: string
  username: string
  displayName: string
  password: string
}

export function uniqueUser(overrides: Partial<Credentials> = {}): Credentials {
  const id = `${Date.now().toString(36)}${Math.random().toString(36).slice(2, 7)}`
  return {
    email: `e2e_${id}@example.com`,
    username: `e2e_${id}`,
    displayName: `Тестер ${id.slice(-4)}`,
    password: 'correct horse battery',
    ...overrides,
  }
}

export function randomIp(): string {
  const octet = () => 1 + Math.floor(Math.random() * 254)
  return `10.${octet()}.${octet()}.${octet()}`
}
