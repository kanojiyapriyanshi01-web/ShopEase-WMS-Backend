// ═══════════════════════════════════════════════════════════
// ShopEase WMS Flutter App — Complete Structure
// Project: shopease_wms (separate from main ShopEase app)
// ═══════════════════════════════════════════════════════════

// ─── lib/main.dart ────────────────────────────────────────

import 'package:flutter/material.dart';
import 'package:provider/provider.dart';
import 'package:go_router/go_router.dart';

import 'core/router.dart';
import 'core/theme.dart';
import 'providers/auth_provider.dart';
import 'providers/order_provider.dart';
import 'providers/inventory_provider.dart';
import 'providers/picking_provider.dart';

void main() async {
  WidgetsFlutterBinding.ensureInitialized();
  runApp(const WMSApp());
}

class WMSApp extends StatelessWidget {
  const WMSApp({super.key});

  @override
  Widget build(BuildContext context) {
    return MultiProvider(
      providers: [
        ChangeNotifierProvider(create: (_) => AuthProvider()),
        ChangeNotifierProvider(create: (_) => OrderProvider()),
        ChangeNotifierProvider(create: (_) => InventoryProvider()),
        ChangeNotifierProvider(create: (_) => PickingProvider()),
      ],
      child: MaterialApp.router(
        title: 'ShopEase WMS',
        theme: WMSTheme.light,
        darkTheme: WMSTheme.dark,
        routerConfig: AppRouter.config,
        debugShowCheckedModeBanner: false,
      ),
    );
  }
}

// ─── lib/core/constants.dart ──────────────────────────────

class AppConstants {
  static const String baseUrl = String.fromEnvironment(
    'WMS_API_URL',
    defaultValue: 'https://shopease-wms.onrender.com/api/v1',
  );

  static const Map<String, List<String>> roleMenus = {
    'super_admin':       ['dashboard', 'orders', 'inventory', 'picking', 'packing', 'dispatch', 'returns', 'transfers', 'analytics', 'staff'],
    'warehouse_manager': ['dashboard', 'orders', 'inventory', 'picking', 'packing', 'dispatch', 'returns', 'transfers', 'analytics'],
    'inventory_staff':   ['dashboard', 'inventory', 'grn'],
    'picker':            ['dashboard', 'picking'],
    'packer':            ['dashboard', 'packing'],
    'dispatcher':        ['dashboard', 'dispatch'],
    'qc_inspector':      ['dashboard', 'returns'],
  };
}

// ─── lib/core/router.dart ─────────────────────────────────

class AppRouter {
  static final config = GoRouter(
    initialLocation: '/login',
    redirect: (context, state) {
      final auth = context.read<AuthProvider>();
      final isLoggedIn = auth.isLoggedIn;
      final isLoginRoute = state.matchedLocation == '/login';

      if (!isLoggedIn && !isLoginRoute) return '/login';
      if (isLoggedIn && isLoginRoute) return '/dashboard';
      return null;
    },
    routes: [
      GoRoute(path: '/login', builder: (ctx, _) => const LoginScreen()),
      ShellRoute(
        builder: (ctx, state, child) => WMSShell(child: child),
        routes: [
          GoRoute(path: '/dashboard',  builder: (ctx, _) => const DashboardScreen()),
          GoRoute(path: '/orders',     builder: (ctx, _) => const OrdersScreen()),
          GoRoute(path: '/orders/:id', builder: (ctx, s) => OrderDetailScreen(id: s.pathParameters['id']!)),
          GoRoute(path: '/inventory',  builder: (ctx, _) => const InventoryScreen()),
          GoRoute(path: '/grn/new',    builder: (ctx, _) => const GRNCreateScreen()),
          GoRoute(path: '/picking',    builder: (ctx, _) => const PickingScreen()),
          GoRoute(path: '/picking/:id/scan', builder: (ctx, s) => ScannerScreen(pickingId: s.pathParameters['id']!)),
          GoRoute(path: '/packing',    builder: (ctx, _) => const PackingScreen()),
          GoRoute(path: '/dispatch',   builder: (ctx, _) => const DispatchScreen()),
          GoRoute(path: '/returns',    builder: (ctx, _) => const ReturnsScreen()),
          GoRoute(path: '/returns/:id/qc', builder: (ctx, s) => QCScreen(returnId: s.pathParameters['id']!)),
          GoRoute(path: '/analytics',  builder: (ctx, _) => const AnalyticsScreen()),
          GoRoute(path: '/transfers',  builder: (ctx, _) => const TransfersScreen()),
          GoRoute(path: '/audits',     builder: (ctx, _) => const AuditsScreen()),
          GoRoute(path: '/staff',      builder: (ctx, _) => const StaffScreen()),
        ],
      ),
    ],
  );
}

// ─── lib/providers/auth_provider.dart ─────────────────────

class AuthProvider extends ChangeNotifier {
  String? _token;
  WMSUser? _user;

  bool get isLoggedIn => _token != null;
  WMSUser? get user => _user;
  String? get token => _token;

  Future<bool> login(String email, String password) async {
    try {
      final response = await ApiService.post('/auth/login', {
        'email': email,
        'password': password,
      });

      _token = response['token'];
      _user = WMSUser.fromJson(response['user']);
      await SecureStorage.write('wms_token', _token!);
      notifyListeners();
      return true;
    } catch (e) {
      return false;
    }
  }

  Future<void> logout() async {
    _token = null;
    _user = null;
    await SecureStorage.delete('wms_token');
    notifyListeners();
  }

  Future<void> restoreSession() async {
    final token = await SecureStorage.read('wms_token');
    if (token != null) {
      _token = token;
      // Optionally fetch user profile
      notifyListeners();
    }
  }
}

// ─── lib/screens/picking/scanner_screen.dart ──────────────
// Most complex screen — barcode scan + verify + update

class ScannerScreen extends StatefulWidget {
  final String pickingId;
  const ScannerScreen({super.key, required this.pickingId});

  @override
  State<ScannerScreen> createState() => _ScannerScreenState();
}

class _ScannerScreenState extends State<ScannerScreen> {
  PickingTask? _task;
  String? _scannedCode;
  bool _isVerifying = false;
  String? _scanResult; // 'correct', 'wrong', 'error'

  @override
  void initState() {
    super.initState();
    _loadTask();
  }

  Future<void> _loadTask() async {
    final task = await PickingService.getTask(widget.pickingId);
    setState(() => _task = task);
  }

  Future<void> _onBarcodeScanned(String code) async {
    if (_isVerifying) return;
    setState(() {
      _isVerifying = true;
      _scannedCode = code;
      _scanResult = null;
    });

    HapticFeedback.lightImpact();

    try {
      final result = await PickingService.scanItem(widget.pickingId, code);
      setState(() {
        _scanResult = result['match'] == true ? 'correct' : 'wrong';
        _isVerifying = false;
      });

      if (_scanResult == 'correct') {
        HapticFeedback.heavyImpact();
        await _loadTask(); // Refresh picking list
      }
    } catch (e) {
      setState(() {
        _scanResult = 'error';
        _isVerifying = false;
      });
    }
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(
        title: const Text('Scan items'),
        actions: [
          TextButton(
            onPressed: _markComplete,
            child: const Text('Mark complete'),
          ),
        ],
      ),
      body: Column(
        children: [
          // Scanner view
          SizedBox(
            height: 280,
            child: MobileScanner(
              onDetect: (capture) {
                final barcode = capture.barcodes.firstOrNull?.rawValue;
                if (barcode != null) _onBarcodeScanned(barcode);
              },
            ),
          ),

          // Scan feedback
          if (_scanResult != null)
            Container(
              width: double.infinity,
              padding: const EdgeInsets.all(12),
              color: _scanResult == 'correct'
                  ? Colors.green.shade100
                  : Colors.red.shade100,
              child: Row(
                children: [
                  Icon(
                    _scanResult == 'correct' ? Icons.check_circle : Icons.error,
                    color: _scanResult == 'correct' ? Colors.green : Colors.red,
                  ),
                  const SizedBox(width: 8),
                  Text(
                    _scanResult == 'correct'
                        ? 'Correct item scanned: $_scannedCode'
                        : 'Wrong item or barcode not found',
                    style: TextStyle(
                      color: _scanResult == 'correct' ? Colors.green.shade800 : Colors.red.shade800,
                    ),
                  ),
                ],
              ),
            ),

          // Picking list
          Expanded(
            child: _task == null
                ? const Center(child: CircularProgressIndicator())
                : ListView.builder(
                    itemCount: _task!.items.length,
                    itemBuilder: (ctx, i) {
                      final item = _task!.items[i];
                      final isPicked = item.qtyPicked >= item.qtyRequired;
                      return ListTile(
                        leading: CircleAvatar(
                          backgroundColor: isPicked ? Colors.green : Colors.grey.shade200,
                          child: Icon(
                            isPicked ? Icons.check : Icons.circle_outlined,
                            color: isPicked ? Colors.white : Colors.grey,
                          ),
                        ),
                        title: Text(item.sku),
                        subtitle: Text('Bin: ${item.binCode}'),
                        trailing: Text(
                          '${item.qtyPicked}/${item.qtyRequired}',
                          style: TextStyle(
                            color: isPicked ? Colors.green : Colors.orange,
                            fontWeight: FontWeight.bold,
                          ),
                        ),
                      );
                    },
                  ),
          ),
        ],
      ),
    );
  }

  Future<void> _markComplete() async {
    final allPicked = _task?.items.every((i) => i.qtyPicked >= i.qtyRequired) ?? false;
    if (!allPicked) {
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('Not all items picked yet')),
      );
      return;
    }

    await PickingService.completePicking(widget.pickingId);
    if (mounted) context.go('/picking');
  }
}

// ─── lib/screens/dashboard/dashboard_screen.dart ──────────

class DashboardScreen extends StatefulWidget {
  const DashboardScreen({super.key});

  @override
  State<DashboardScreen> createState() => _DashboardScreenState();
}

class _DashboardScreenState extends State<DashboardScreen> {
  DashboardStats? _stats;
  bool _loading = true;

  @override
  void initState() {
    super.initState();
    _loadStats();
    // Auto-refresh every 30s
    Timer.periodic(const Duration(seconds: 30), (_) => _loadStats());
  }

  Future<void> _loadStats() async {
    try {
      final stats = await AnalyticsService.getDashboard();
      setState(() {
        _stats = stats;
        _loading = false;
      });
    } catch (e) {
      setState(() => _loading = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(
        title: const Text('Warehouse dashboard'),
        actions: [
          IconButton(icon: const Icon(Icons.refresh), onPressed: _loadStats),
        ],
      ),
      body: _loading
          ? const Center(child: CircularProgressIndicator())
          : RefreshIndicator(
              onRefresh: _loadStats,
              child: GridView.count(
                crossAxisCount: 2,
                padding: const EdgeInsets.all(16),
                crossAxisSpacing: 12,
                mainAxisSpacing: 12,
                childAspectRatio: 1.5,
                children: [
                  _StatCard('Orders today',   _stats?.ordersToday ?? 0,  Colors.blue,   '/orders?filter=today'),
                  _StatCard('Pending',        _stats?.pendingOrders ?? 0, Colors.orange, '/orders?status=received'),
                  _StatCard('In picking',     _stats?.inPicking ?? 0,    Colors.purple, '/picking'),
                  _StatCard('In packing',     _stats?.inPacking ?? 0,    Colors.indigo, '/packing'),
                  _StatCard('Ready to ship',  _stats?.readyToShip ?? 0,  Colors.teal,   '/dispatch'),
                  _StatCard('Shipped today',  _stats?.shippedToday ?? 0, Colors.green,  '/orders?status=shipped'),
                  _StatCard('Returns',        _stats?.returns ?? 0,      Colors.red,    '/returns'),
                  _StatCard('Low stock SKUs', _stats?.lowStockSkus ?? 0, Colors.amber,  '/inventory?low_stock=true'),
                ],
              ),
            ),
    );
  }
}

class _StatCard extends StatelessWidget {
  final String label;
  final int value;
  final Color color;
  final String route;

  const _StatCard(this.label, this.value, this.color, this.route);

  @override
  Widget build(BuildContext context) {
    return GestureDetector(
      onTap: () => context.go(route),
      child: Card(
        elevation: 0,
        shape: RoundedRectangleBorder(
          borderRadius: BorderRadius.circular(12),
          side: BorderSide(color: color.withOpacity(0.3)),
        ),
        child: Container(
          decoration: BoxDecoration(
            borderRadius: BorderRadius.circular(12),
            color: color.withOpacity(0.05),
          ),
          padding: const EdgeInsets.all(16),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            mainAxisAlignment: MainAxisAlignment.spaceBetween,
            children: [
              Text(
                label,
                style: TextStyle(fontSize: 12, color: Colors.grey.shade600),
              ),
              Text(
                value.toString(),
                style: TextStyle(fontSize: 28, fontWeight: FontWeight.bold, color: color),
              ),
            ],
          ),
        ),
      ),
    );
  }
}

// ─── lib/services/api_service.dart ────────────────────────

class ApiService {
  static final _dio = Dio(BaseOptions(baseUrl: AppConstants.baseUrl));
  static String? _token;

  static void setToken(String token) {
    _token = token;
    _dio.options.headers['Authorization'] = 'Bearer $token';
  }

  static Future<Map<String, dynamic>> post(String path, Map<String, dynamic> body) async {
    final response = await _dio.post(path, data: body);
    return response.data as Map<String, dynamic>;
  }

  static Future<Map<String, dynamic>> get(String path, {Map<String, dynamic>? params}) async {
    final response = await _dio.get(path, queryParameters: params);
    return response.data as Map<String, dynamic>;
  }

  static Future<Map<String, dynamic>> put(String path, Map<String, dynamic> body) async {
    final response = await _dio.put(path, data: body);
    return response.data as Map<String, dynamic>;
  }
}

// ─── pubspec.yaml ─────────────────────────────────────────

/*
name: shopease_wms
description: ShopEase Warehouse Management System

environment:
  sdk: ">=3.0.0 <4.0.0"
  flutter: ">=3.10.0"

dependencies:
  flutter:
    sdk: flutter
  
  # State management & navigation
  provider: ^6.1.2
  go_router: ^14.0.0
  
  # HTTP & storage
  dio: ^5.4.3
  flutter_secure_storage: ^9.0.0
  hive: ^2.2.3
  hive_flutter: ^1.1.0
  
  # Barcode / QR scanning
  mobile_scanner: ^5.1.0
  
  # PDF generation (labels, invoices)
  pdf: ^3.10.8
  printing: ^5.12.0
  
  # Charts & analytics
  fl_chart: ^0.68.0
  
  # Push notifications
  firebase_core: ^2.27.0
  firebase_messaging: ^14.7.20
  
  # UI utilities
  shimmer: ^3.0.0
  intl: ^0.19.0
  cached_network_image: ^3.3.1
  qr_flutter: ^4.1.0
  
  # Offline sync
  connectivity_plus: ^6.0.1
  
dev_dependencies:
  flutter_test:
    sdk: flutter
  hive_generator: ^2.0.1
  build_runner: ^2.4.9
*/

// ─── lib/core/theme.dart ──────────────────────────────────

class WMSTheme {
  static final light = ThemeData(
    useMaterial3: true,
    colorScheme: ColorScheme.fromSeed(
      seedColor: const Color(0xFF1A73E8),
      brightness: Brightness.light,
    ),
    cardTheme: const CardTheme(elevation: 0),
    appBarTheme: const AppBarTheme(elevation: 0, centerTitle: false),
  );

  static final dark = ThemeData(
    useMaterial3: true,
    colorScheme: ColorScheme.fromSeed(
      seedColor: const Color(0xFF1A73E8),
      brightness: Brightness.dark,
    ),
    cardTheme: const CardTheme(elevation: 0),
    appBarTheme: const AppBarTheme(elevation: 0, centerTitle: false),
  );
}
