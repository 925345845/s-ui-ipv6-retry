// Composables
import { createRouter, createWebHistory } from 'vue-router'
import Login from '@/views/Login.vue'
import Data from '@/store/modules/data'

const routes = [
  {
    path: '/login',
    name: 'pages.login',
    component: Login,
  },
  {
    path: '/',
    component: () => import('@/layouts/default/Default.vue'),
    meta: { requiresAuth: true },
    children: [
      {
        path: '/',
        name: 'pages.home',
        component: () => import('@/views/Home.vue'),
      },
      {
        path: '/inbounds',
        name: 'pages.inbounds',
        component: () => import('@/views/Inbounds.vue'),
      },
      {
        path: '/clients',
        name: 'pages.clients',
        component: () => import('@/views/Clients.vue'),
      },  
      {
        path: '/outbounds',
        name: 'pages.outbounds',
        component: () => import('@/views/Outbounds.vue'),
      },
      {
        path: '/services',
        name: 'pages.services',
        component: () => import('@/views/Services.vue'),
      },
      {
        path: '/endpoints',
        name: 'pages.endpoints',
        component: () => import('@/views/Endpoints.vue'),
      },
      {
        path: '/rules',
        name: 'pages.rules',
        component: () => import('@/views/Rules.vue'),
      },
      {
        path: '/tls',
        name: 'pages.tls',
        component: () => import('@/views/Tls.vue'),
      },
      {
        path: '/basics',
        name: 'pages.basics',
        component: () => import('@/views/Basics.vue'),
      },
      {
        path: '/dns',
        name: 'pages.dns',
        component: () => import('@/views/Dns.vue'),
      },
      {
        path: '/admins',
        name: 'pages.admins',
        component: () => import('@/views/Admins.vue'),
      },
      {
        path: '/settings',
        name: 'pages.settings',
        component: () => import('@/views/Settings.vue'),
      },
      {
        path: '/agents',
        name: 'pages.agents',
        component: () => import('@/views/Agents.vue'),
      },
      {
        path: '/agents/:id',
        name: 'agent.detail',
        component: () => import('@/views/AgentDetail.vue'),
      },
      {
        path: '/agents/:id/inbounds',
        name: 'agent.remoteInbounds',
        component: () => import('@/views/AgentInbounds.vue'),
      },
    ],
  },
]

const router = createRouter({
  history: createWebHistory((window as any).BASE_URL),
  routes,
})

const DATA_REFRESH_MS = 15000
let intervalId: ReturnType<typeof setInterval> | undefined

const refreshData = () => {
  if (!document.hidden) void Data().loadData()
}

router.beforeEach((to) => {
  // The server and API own authentication. The session cookie is HttpOnly and
  // intentionally unavailable to client-side routing.
  if (to.path !== '/login') {
    loadDataInterval()
  } else {
    if (intervalId) {
      clearInterval(intervalId)
      intervalId = undefined
    }
  }
})

const loadDataInterval = () => {
  if (intervalId) return
  refreshData()
  intervalId = setInterval(refreshData, DATA_REFRESH_MS)
}

document.addEventListener('visibilitychange', () => {
  if (intervalId && !document.hidden) refreshData()
})

export default router
