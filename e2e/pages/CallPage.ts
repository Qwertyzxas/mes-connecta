import type { Locator, Page } from '@playwright/test'

export class CallPage {
  readonly audioCall: Locator
  readonly videoCall: Locator
  readonly screen: Locator
  readonly status: Locator
  readonly hangUp: Locator
  readonly muteMic: Locator
  readonly unmuteMic: Locator
  readonly cameraOff: Locator
  readonly cameraOn: Locator
  readonly self: Locator
  readonly participants: Locator
  readonly incoming: Locator
  readonly accept: Locator
  readonly decline: Locator
  readonly messages: Locator

  constructor(readonly page: Page) {
    this.audioCall = page.getByRole('button', { name: 'Аудиозвонок' })
    this.videoCall = page.getByRole('button', { name: 'Видеозвонок' })
    this.screen = page.getByRole('dialog', { name: 'Звонок' })
    this.status = page.getByTestId('call-status')
    this.hangUp = page.getByRole('button', { name: 'Завершить звонок' })
    this.muteMic = page.getByRole('button', { name: 'Выключить микрофон' })
    this.unmuteMic = page.getByRole('button', { name: 'Включить микрофон' })
    this.cameraOff = page.getByRole('button', { name: 'Выключить камеру' })
    this.cameraOn = page.getByRole('button', { name: 'Включить камеру' })
    this.self = page.getByTestId('call-self')
    this.participants = page.getByTestId('call-participant')
    this.incoming = page.getByRole('alertdialog', { name: /^Входящий (видео)?звонок$/ })
    this.accept = this.incoming.getByRole('button', { name: 'Принять' })
    this.decline = this.incoming.getByRole('button', { name: 'Отклонить' })
    this.messages = page.getByTestId('call-message')
  }

  participant(name: string): Locator {
    return this.participants.filter({ hasText: name })
  }
}
