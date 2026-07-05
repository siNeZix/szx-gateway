import { QueryClientProvider } from '@tanstack/react-query'
import { BrowserRouter, Route, Routes } from 'react-router-dom'
import { queryClient } from './providers/query'
import Layout from './components/layout/Layout'
import Dashboard from './pages/Dashboard'
import Keys from './pages/Keys'
import Stats from './pages/Stats'
import Models from './pages/Models'
import Proxies from './pages/Proxies'

export default function App() {
  return (
    <QueryClientProvider client={queryClient}>
      <BrowserRouter>
        <Routes>
          <Route element={<Layout />}>
            <Route path="/" element={<Dashboard />} />
            <Route path="/keys" element={<Keys />} />
            <Route path="/stats" element={<Stats />} />
            <Route path="/models" element={<Models />} />
            <Route path="/proxies" element={<Proxies />} />
          </Route>
        </Routes>
      </BrowserRouter>
    </QueryClientProvider>
  )
}
