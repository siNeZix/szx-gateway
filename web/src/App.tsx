import { QueryClientProvider } from '@tanstack/react-query'
import { useEffect, useState } from 'react'
import { BrowserRouter, Route, Routes } from 'react-router-dom'
import { queryClient } from './providers/query'
import Layout from './components/layout/Layout'
import Dashboard from './pages/Dashboard'
import Keys from './pages/Keys'
import Stats from './pages/Stats'
import Models from './pages/Models'
import Proxies from './pages/Proxies'
import Status from './pages/Status'
import Login from './pages/Login'
import { api } from './api/client'

export default function App() {
  const [authState, setAuthState] = useState<'checking' | 'authenticated' | 'unauthenticated'>('checking')

  useEffect(() => {
    api.checkAuth().then(
      () => setAuthState('authenticated'),
      () => setAuthState('unauthenticated'),
    )
  }, [])

  if (authState === 'checking') return null

  if (authState === 'unauthenticated') {
    return (
      <Login
        onLogin={async (username, password) => {
          await api.login(username, password)
          setAuthState('authenticated')
        }}
      />
    )
  }

	return (
    <QueryClientProvider client={queryClient}>
      <BrowserRouter>
        <Routes>
          <Route element={<Layout />}>
            <Route path="/" element={<Dashboard />} />
            <Route path="/keys" element={<Keys />} />
            <Route path="/stats" element={<Stats />} />
            <Route path="/models" element={<Models />} />
			<Route path="/status" element={<Status />} />
            <Route path="/proxies" element={<Proxies />} />
          </Route>
        </Routes>
      </BrowserRouter>
    </QueryClientProvider>
  )
}
