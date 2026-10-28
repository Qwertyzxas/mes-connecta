export const APP_NAME = 'Connecta'

type LogoProps = {
  size?: number
}

export function Logo({ size = 40 }: LogoProps) {
  return (
    <div className="logo">
      <img src="/logo-mark.png" width={size} height={size} alt="" className="logo__mark" />
      <span className="logo__name">{APP_NAME}</span>
    </div>
  )
}
