import { Route, Routes } from 'react-router-dom';
import { DashboardProvider } from './AppContext';
import { Layout } from './components/Layout';
import { DialectDetailPage } from './pages/DialectDetailPage';
import { DialectFormPage } from './pages/DialectFormPage';
import { LaunchersPage } from './pages/LaunchersPage';
import { NotFoundPage } from './pages/NotFoundPage';
import { OverviewPage } from './pages/OverviewPage';
import { RuntimePage } from './pages/RuntimePage';

export default function App() {
  return (
    <DashboardProvider>
      <Routes>
        <Route element={<Layout />}>
          <Route index element={<OverviewPage />} />
          <Route path="dialects/new" element={<DialectFormPage />} />
          <Route path="dialects/:name" element={<DialectDetailPage />} />
          <Route path="dialects/:name/edit" element={<DialectFormPage />} />
          <Route path="launchers" element={<LaunchersPage />} />
          <Route path="runtime" element={<RuntimePage />} />
          <Route path="*" element={<NotFoundPage />} />
        </Route>
      </Routes>
    </DashboardProvider>
  );
}
