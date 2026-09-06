#!/usr/bin/env node
import puppeteer from 'puppeteer-core';

const CHROME_PATH = '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome';

async function testBrowser() {
  console.log('🚀 启动 Chrome 浏览器...');
  const browser = await puppeteer.launch({
    executablePath: CHROME_PATH,
    headless: false, // 设为 true 可无头运行
    args: ['--start-maximized']
  });

  const page = await browser.newPage();
  
  // 访问本地开发服务器
  console.log('📄 访问 http://localhost:3000 ...');
  await page.goto('http://localhost:3000', { waitUntil: 'networkidle2' });
  
  console.log('✅ 浏览器测试成功！');
  console.log(`📝 页面标题: ${await page.title()}`);
  
  // 保持浏览器打开 5 秒供检查
  await new Promise(r => setTimeout(r, 5000));
  
  await browser.close();
  console.log('🏁 测试完成');
}

testBrowser().catch(console.error);
