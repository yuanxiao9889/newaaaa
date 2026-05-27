/*
Copyright (C) 2025 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/

import React, { useContext, useEffect, useMemo, useState } from 'react';
import { Link } from 'react-router-dom';
import { marked } from 'marked';
import { useTranslation } from 'react-i18next';
import {
  ArrowRight,
  BarChart3,
  Box,
  Code2,
  FileText,
  Github,
  Layers3,
  LogIn,
  Mail,
  Menu,
  MessageCircle,
  Orbit,
  ShieldCheck,
  Sparkles,
  UsersRound,
  X,
} from 'lucide-react';
import { API } from '../../helpers';
import { useIsMobile } from '../../hooks/common/useIsMobile';
import { StatusContext } from '../../context/Status';
import { UserContext } from '../../context/User';
import { useActualTheme } from '../../context/Theme';
import NoticeModal from '../../components/layout/NoticeModal';

const LANDING_ASSETS = {
  logo: '/logo.png',
  hero: '/landing/hero-infinity.webp',
  featureUnified: '/landing/feature-unified.png',
  featureStability: '/landing/feature-stability.png',
  featureDeveloper: '/landing/feature-developer.png',
  ctaAurora: '/landing/cta-aurora.png',
  heroIntroVideo: '/landing/hero-intro.mp4',
  heroLoopVideo: '/landing/hero-loop.mp4',
};

const emitHomeShellMode = (hideShell) => {
  window.dispatchEvent(
    new CustomEvent('newapi:home-shell-mode', {
      detail: { hideShell },
    }),
  );
};

const InfinityGlyph = ({ compact = false }) => (
  <svg
    className={compact ? 'landing-infinity landing-infinity-compact' : 'landing-infinity'}
    viewBox='0 0 220 120'
    aria-hidden='true'
  >
    <defs>
      <filter id={compact ? 'landingLogoGlow' : 'landingHeroGlow'}>
        <feGaussianBlur stdDeviation={compact ? '3' : '7'} result='blur' />
        <feMerge>
          <feMergeNode in='blur' />
          <feMergeNode in='SourceGraphic' />
        </feMerge>
      </filter>
      <linearGradient id={compact ? 'landingLogoSheen' : 'landingHeroSheen'} x1='0%' y1='0%' x2='100%' y2='100%'>
        <stop offset='0%' stopColor='#ffffff' />
        <stop offset='44%' stopColor='#f8fbff' />
        <stop offset='72%' stopColor='#e9f0ff' />
        <stop offset='100%' stopColor='#ffffff' />
      </linearGradient>
    </defs>
    <path
      d='M26 62C26 27 66 21 110 60C154 99 194 94 194 58C194 25 154 20 110 60C66 100 26 95 26 62Z'
      fill='none'
      stroke={`url(#${compact ? 'landingLogoSheen' : 'landingHeroSheen'})`}
      strokeWidth={compact ? '24' : '30'}
      strokeLinecap='round'
      strokeLinejoin='round'
      filter={`url(#${compact ? 'landingLogoGlow' : 'landingHeroGlow'})`}
    />
    {[
      [52, 37, -19],
      [72, 41, 22],
      [92, 53, 38],
      [128, 53, -39],
      [148, 42, -22],
      [168, 38, 18],
      [47, 77, 14],
      [72, 78, -25],
      [148, 78, 25],
      [173, 76, -13],
    ].map(([x, y, rotate], index) => (
      <rect
        key={index}
        x={x}
        y={y}
        width={compact ? '8' : '11'}
        height={compact ? '6' : '9'}
        rx='1.5'
        fill='#1b2433'
        opacity='0.72'
        transform={`rotate(${rotate} ${x} ${y})`}
      />
    ))}
  </svg>
);

const LandingAsset = ({ src, alt, className, fallback, decorative = false }) => {
  const sources = Array.isArray(src) ? src : [src];
  const [sourceIndex, setSourceIndex] = useState(0);
  const currentSource = sources[sourceIndex];
  const failed = sourceIndex >= sources.length;

  if (failed || !currentSource) {
    return fallback || null;
  }

  return (
    <img
      src={currentSource}
      alt={decorative ? '' : alt}
      aria-hidden={decorative}
      className={className}
      onError={() => setSourceIndex((index) => index + 1)}
      loading='lazy'
    />
  );
};

const FeatureFallback = ({ tone, icon: Icon }) => (
  <div className={`landing-feature-visual landing-feature-visual-${tone}`}>
    <div className='landing-feature-orb' />
    <Icon size={58} strokeWidth={1.4} />
  </div>
);

const Home = () => {
  const { t, i18n } = useTranslation();
  const [statusState] = useContext(StatusContext);
  const [userState] = useContext(UserContext);
  const actualTheme = useActualTheme();
  const [homePageContentLoaded, setHomePageContentLoaded] = useState(false);
  const [homePageContent, setHomePageContent] = useState('');
  const [noticeVisible, setNoticeVisible] = useState(false);
  const [mobileMenuOpen, setMobileMenuOpen] = useState(false);
  const [isVisible, setIsVisible] = useState(false);
  const [heroVideoPhase, setHeroVideoPhase] = useState('intro');
  const isMobile = useIsMobile();
  const docsLink = statusState?.status?.docs_link || '';
  const isLoggedIn = Boolean(userState?.user);
  const actionTarget = isLoggedIn ? '/console' : '/register';
  const isChinese = i18n.language.startsWith('zh');

  const starField = useMemo(
    () =>
      Array.from({ length: 72 }, (_, index) => ({
        id: index,
        left: (index * 37) % 100,
        top: (index * 61) % 100,
        delay: `${(index % 9) * 0.35}s`,
        size: index % 6 === 0 ? 2 : 1,
        opacity: 0.18 + (index % 5) * 0.09,
      })),
    [],
  );

  const navItems = useMemo(
    () => [
      { label: t('产品'), href: '#product', active: true },
      { label: t('解决方案'), href: '#solutions' },
      { label: t('定价'), to: '/pricing' },
      docsLink
        ? { label: t('文档'), href: docsLink, external: true }
        : { label: t('文档'), to: '/console/token' },
      { label: t('开发者'), to: isLoggedIn ? '/console' : '/register' },
      { label: t('关于'), to: '/about' },
    ],
    [docsLink, isLoggedIn, t],
  );

  const features = useMemo(
    () => [
      {
        title: t('统一接入'),
        desc: t('标准化 API 接口，一次接入即可调用多家优质大模型能力。'),
        asset: LANDING_ASSETS.featureUnified,
        tone: 'cyan',
        icon: Box,
      },
      {
        title: t('稳定高效'),
        desc: t('智能路由与负载均衡，保障请求稳定性与低延迟响应。'),
        asset: LANDING_ASSETS.featureStability,
        tone: 'violet',
        icon: Orbit,
      },
      {
        title: t('开发者体验'),
        desc: t('完善的 SDK、文档与调试工具，提升开发效率与集成体验。'),
        asset: LANDING_ASSETS.featureDeveloper,
        tone: 'gold',
        icon: Code2,
      },
    ],
    [t],
  );

  const stats = useMemo(
    () => [
      { icon: ShieldCheck, value: '99.95%', label: t('服务可用性'), tone: 'blue' },
      { icon: Layers3, value: '300+', label: t('接入模型'), tone: 'purple' },
      { icon: UsersRound, value: '10,000+', label: t('开发者选择'), tone: 'green' },
      { icon: BarChart3, value: '1B+', label: t('每日请求量'), tone: 'lime' },
    ],
    [t],
  );

  const footerGroups = useMemo(
    () => [
      {
        title: t('产品'),
        links: [
          { label: t('API 文档'), href: docsLink || '/console/token', external: Boolean(docsLink) },
          { label: t('模型列表'), href: '/pricing' },
          { label: t('定价'), href: '/pricing' },
          { label: t('更新日志'), href: '/about' },
        ],
      },
      {
        title: t('解决方案'),
        links: [
          { label: t('企业服务'), href: '/about' },
          { label: t('行业方案'), href: '#solutions' },
          { label: t('应用场景'), href: '#product' },
          { label: t('案例中心'), href: '#product' },
        ],
      },
      {
        title: t('支持'),
        links: [
          { label: t('帮助中心'), href: docsLink || '/about', external: Boolean(docsLink) },
          { label: t('状态页面'), href: '/about' },
          { label: t('联系支持'), href: 'mailto:support@quantumnous.com', external: true },
          { label: t('反馈建议'), href: '/about' },
        ],
      },
    ],
    [docsLink, t],
  );

  const displayHomePageContent = async () => {
    const cachedContent = localStorage.getItem('home_page_content') || '';
    setHomePageContent(cachedContent);
    emitHomeShellMode(cachedContent === '');

    try {
      const res = await API.get('/api/home_page_content');
      const { success, message, data } = res.data;

      if (success) {
        let content = data || '';
        if (content && !content.startsWith('https://')) {
          content = marked.parse(content);
        }
        setHomePageContent(content);
        localStorage.setItem('home_page_content', content);
        emitHomeShellMode(content === '');

        if (data?.startsWith('https://')) {
          setTimeout(() => {
            const iframe = document.querySelector('iframe[data-home-content]');
            if (iframe?.contentWindow) {
              iframe.contentWindow.postMessage({ themeMode: actualTheme }, '*');
              iframe.contentWindow.postMessage({ lang: i18n.language }, '*');
            }
          });
        }
      } else {
        console.warn('加载首页内容失败:', message);
        setHomePageContent(cachedContent);
        emitHomeShellMode(cachedContent === '');
      }
    } catch (error) {
      console.warn('加载首页内容失败:', error);
      setHomePageContent(cachedContent);
      emitHomeShellMode(cachedContent === '');
    }
    setHomePageContentLoaded(true);
  };

  const checkNoticeAndShow = async () => {
    const lastCloseDate = localStorage.getItem('notice_close_date');
    const today = new Date().toDateString();
    if (lastCloseDate === today) {
      return;
    }

    try {
      const res = await API.get('/api/notice');
      const { success, data } = res.data;
      if (success && data && data.trim() !== '') {
        setNoticeVisible(true);
      }
    } catch (error) {
      console.error('获取公告失败:', error);
    }
  };

  useEffect(() => {
    checkNoticeAndShow();
  }, []);

  useEffect(() => {
    displayHomePageContent().then();
    const timer = setTimeout(() => setIsVisible(true), 120);

    return () => {
      clearTimeout(timer);
      emitHomeShellMode(false);
    };
  }, []);

  const renderNavLink = (item, className = 'landing-nav-link') => {
    const classes = `${className}${item.active ? ' is-active' : ''}`;

    if (item.to) {
      return (
        <Link key={item.label} to={item.to} className={classes} onClick={() => setMobileMenuOpen(false)}>
          {item.label}
        </Link>
      );
    }

    return (
      <a
        key={item.label}
        href={item.href}
        className={classes}
        target={item.external ? '_blank' : undefined}
        rel={item.external ? 'noopener noreferrer' : undefined}
        onClick={() => setMobileMenuOpen(false)}
      >
        {item.label}
      </a>
    );
  };

  const docsButton = docsLink ? (
    <a href={docsLink} target='_blank' rel='noopener noreferrer' className='landing-button landing-button-ghost'>
      <FileText size={22} />
      {t('查看文档')}
    </a>
  ) : (
    <Link to='/console/token' className='landing-button landing-button-ghost'>
      <FileText size={22} />
      {t('查看文档')}
    </Link>
  );

  if (!homePageContentLoaded) {
    return <div className='landing-loading' />;
  }

  return (
    <div className='landing-page-shell'>
      <NoticeModal
        visible={noticeVisible}
        onClose={() => setNoticeVisible(false)}
        isMobile={isMobile}
      />

      {homePageContent === '' ? (
        <main className='landing-page'>
          <div className='landing-noise' aria-hidden='true' />
          <div className='landing-spotlight landing-spotlight-gold' aria-hidden='true' />
          <div className='landing-spotlight landing-spotlight-blue' aria-hidden='true' />
          <div className='landing-stars' aria-hidden='true'>
            {starField.map((star) => (
              <span
                key={star.id}
                style={{
                  left: `${star.left}%`,
                  top: `${star.top}%`,
                  width: star.size,
                  height: star.size,
                  opacity: star.opacity,
                  animationDelay: star.delay,
                }}
              />
            ))}
          </div>

          <header className='landing-header'>
            <Link to='/' className='landing-brand' aria-label={t('首页')}>
              <LandingAsset
                src={LANDING_ASSETS.logo}
                alt={t('模型无限 API 平台')}
                className='landing-brand-image'
                fallback={<InfinityGlyph compact />}
              />
              <span className='landing-brand-text'>pii</span>
            </Link>

            <nav className='landing-nav' aria-label={t('首页导航')}>
              {navItems.map((item) => renderNavLink(item))}
            </nav>

            <div className='landing-header-actions'>
              {isLoggedIn ? (
                <Link to='/console' className='landing-login-link'>
                  {t('控制台')}
                </Link>
              ) : (
                <Link to='/login' className='landing-login-link'>
                  <LogIn size={20} />
                  {t('登录')}
                </Link>
              )}
              <Link to={isLoggedIn ? '/console' : '/register'} className='landing-register-button'>
                {isLoggedIn ? t('进入控制台') : t('注册')}
              </Link>
              <button
                type='button'
                className='landing-menu-button'
                aria-label={mobileMenuOpen ? t('关闭菜单') : t('打开菜单')}
                onClick={() => setMobileMenuOpen((open) => !open)}
              >
                {mobileMenuOpen ? <X size={24} /> : <Menu size={24} />}
              </button>
            </div>
          </header>

          {mobileMenuOpen && (
            <div className='landing-mobile-menu'>
              {navItems.map((item) => renderNavLink(item, 'landing-mobile-nav-link'))}
              {!isLoggedIn && (
                <Link to='/login' className='landing-mobile-nav-link' onClick={() => setMobileMenuOpen(false)}>
                  {t('登录')}
                </Link>
              )}
            </div>
          )}

          <section className={`landing-hero ${isVisible ? 'is-visible' : ''}`} id='product'>
            <div className='landing-hero-video-wrap' aria-hidden='true'>
              <video
                key={heroVideoPhase}
                className='landing-hero-video'
                src={
                  heroVideoPhase === 'intro'
                    ? LANDING_ASSETS.heroIntroVideo
                    : LANDING_ASSETS.heroLoopVideo
                }
                autoPlay
                muted
                playsInline
                loop={heroVideoPhase === 'loop'}
                preload='auto'
                onEnded={() => setHeroVideoPhase('loop')}
                onError={() => setHeroVideoPhase('loop')}
              />
            </div>
            <div className='landing-hero-copy'>
              <h1 className={isChinese ? 'landing-title landing-title-cn' : 'landing-title'}>
                <span>{t('连接无限想象')}</span>
                <span>
                  {t('释放')} <strong>AI</strong> {t('模型的全部')}<strong>{t('潜能')}</strong>
                </span>
              </h1>
              <p className='landing-subtitle'>
                <span>{t('稳定、易用、可扩展的模型接入平台')}</span>
                <span>{t('让开发者专注于创造价值')}</span>
              </p>
              <div className='landing-hero-actions'>
                <Link to={actionTarget} className='landing-button landing-button-primary'>
                  {t('立即体验')}
                  <ArrowRight size={24} />
                </Link>
                {docsButton}
              </div>
            </div>
          </section>

          <section className='landing-features' id='solutions'>
            {features.map((feature) => (
              <article className={`landing-feature-card landing-feature-card-${feature.tone}`} key={feature.title}>
                <div>
                  <h2>{feature.title}</h2>
                  <p>{feature.desc}</p>
                </div>
                <a className='landing-feature-arrow' href={feature.title === t('统一接入') ? '#product' : '#stats'} aria-label={feature.title}>
                  <ArrowRight size={24} />
                </a>
                <LandingAsset
                  src={feature.asset}
                  alt={feature.title}
                  className='landing-feature-image'
                  fallback={<FeatureFallback tone={feature.tone} icon={feature.icon} />}
                />
              </article>
            ))}
          </section>

          <div className='landing-bottom-stage'>
            <section className='landing-stats' id='stats' aria-label={t('平台数据')}>
              {stats.map((stat) => {
                const Icon = stat.icon;
                return (
                  <div className='landing-stat-item' key={stat.label}>
                    <div className={`landing-stat-icon landing-stat-icon-${stat.tone}`}>
                      <Icon size={40} strokeWidth={1.8} />
                    </div>
                    <div>
                      <strong>{stat.value}</strong>
                      <span>{stat.label}</span>
                    </div>
                  </div>
                );
              })}
            </section>

            <section className='landing-cta'>
              <Sparkles className='landing-cta-star' size={36} fill='currentColor' strokeWidth={1.4} />
              <h2>{t('让模型集成更简单')}</h2>
              <p>{t('从一个统一入口，连接无限可能。')}</p>
              <Link to={actionTarget} className='landing-button landing-button-cta'>
                {t('开始构建')}
                <ArrowRight size={24} />
              </Link>
            </section>
          </div>

          <footer className='landing-footer'>
            <div className='landing-footer-main'>
              <div className='landing-footer-brand'>
                <LandingAsset
                  src={LANDING_ASSETS.logo}
                  alt={t('模型无限 API 平台')}
                  className='landing-footer-logo'
                  fallback={<InfinityGlyph compact />}
                />
                <p>{t('统一的大模型接入平台，为开发者提供稳定高效的模型服务与工具。')}</p>
                <span>© 2026 {t('模型无限 API 平台')}</span>
              </div>

              <div className='landing-footer-links'>
                {footerGroups.map((group) => (
                  <div className='landing-footer-column' key={group.title}>
                    <h3>{group.title}</h3>
                    {group.links.map((link) =>
                      link.external || link.href.startsWith('#') || link.href.startsWith('mailto:') ? (
                        <a
                          key={link.label}
                          href={link.href}
                          target={link.external && !link.href.startsWith('mailto:') ? '_blank' : undefined}
                          rel={link.external && !link.href.startsWith('mailto:') ? 'noopener noreferrer' : undefined}
                        >
                          {link.label}
                        </a>
                      ) : (
                        <Link key={link.label} to={link.href}>
                          {link.label}
                        </Link>
                      ),
                    )}
                  </div>
                ))}
              </div>

              <div className='landing-socials' aria-label={t('社交链接')}>
                <a href='https://github.com/QuantumNous/new-api' target='_blank' rel='noopener noreferrer' aria-label='GitHub'>
                  <Github size={28} />
                </a>
                <a href={docsLink || '/about'} target={docsLink ? '_blank' : undefined} rel={docsLink ? 'noopener noreferrer' : undefined} aria-label='Discord'>
                  <MessageCircle size={28} />
                </a>
                <a href='mailto:support@quantumnous.com' aria-label='Email'>
                  <Mail size={28} />
                </a>
                <a href='mailto:support@quantumnous.com' aria-label={t('联系支持')}>
                  <Mail size={28} />
                </a>
              </div>
            </div>

            <div className='landing-footer-bottom'>
              <Link to='/privacy-policy'>{t('隐私政策')}</Link>
              <Link to='/user-agreement'>{t('服务条款')}</Link>
              <a href='#product'>{t('Cookie 设置')}</a>
            </div>
          </footer>
        </main>
      ) : (
        <div className='overflow-x-hidden w-full'>
          {homePageContent.startsWith('https://') ? (
            <iframe
              src={homePageContent}
              className='w-full h-screen border-none'
              data-home-content
              title={t('自定义首页')}
            />
          ) : (
            <div
              className='mt-[60px]'
              dangerouslySetInnerHTML={{ __html: homePageContent }}
            />
          )}
        </div>
      )}
    </div>
  );
};

export default Home;
