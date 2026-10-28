import { useEffect } from 'react'
import { BrowserRouter, Navigate, Outlet, Route, Routes } from 'react-router'
import { HomePage } from './pages/HomePage'
import { LoginPage } from './pages/LoginPage'
import { RegisterPage } from './pages/RegisterPage'
import { useAuth } from './store/auth'

function RequireAuth() {
  const status = useAuth((s) => s.status)
  return status === 'authenticated' ? <Outlet /> : <Navigate to="/login" replace />
}

function GuestOnly() {
  const status = useAuth((s) => s.status)
  return status === 'guest' ? <Outlet /> : <Navigate to="/" replace />
}

function Splash() {
  return (
    <div className="splash" aria-busy="true" aria-label="Загрузка">
      <img src="/logo-mark.png" alt="" className="splash__mark" />
    </div>
  )
}

export function App() {
  const status = useAuth((s) => s.status)
  const bootstrap = useAuth((s) => s.bootstrap)

  useEffect(() => {
    void bootstrap()
  }, [bootstrap])

  if (status === 'loading') return <Splash />

  return (
    <BrowserRouter>
      <Routes>
        <Route element={<GuestOnly />}>
          <Route path="/login" element={<LoginPage />} />
          <Route path="/register" element={<RegisterPage />} />
        </Route>
        <Route element={<RequireAuth />}>
          <Route path="/" element={<HomePage />} />
        </Route>
        <Route path="*" element={<Navigate to="/" replace />} />
      </Routes>
    </BrowserRouter>
  )
}
